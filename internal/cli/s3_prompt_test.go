//go:build !windows

package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/sliplane/cli/internal/auth"
	"github.com/sliplane/cli/internal/config"
	"github.com/zalando/go-keyring"
)

func TestS3FirstUse(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	for _, name := range []string{"SLIPLANE_S3_ACCESS_KEY_ID", "SLIPLANE_S3_SECRET_ACCESS_KEY"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	for _, scenario := range []string{"ls", "upload", "download", "decline", "enter", "json", "piped output", "piped input", "piped error", "read-only", "keychain failure", "verification failed", "permissions activating"} {
		t.Run(scenario, func(t *testing.T) {
			keyring.MockInit()
			if scenario == "keychain failure" {
				keyring.MockInitWithError(fmt.Errorf("keychain locked"))
			}
			configPath := filepath.Join(t.TempDir(), "config.json")
			preferences := config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org_work", CredentialID: "work-key"}}}
			if err := config.Save(configPath, preferences); err != nil {
				t.Fatal(err)
			}
			var creates, deletes, transfers, probes atomic.Int32
			objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), "bucket-access/") {
					t.Error("object request did not use the newly saved S3 key")
				}
				if r.URL.Query().Get("max-keys") == "1" {
					attempt := probes.Add(1)
					if scenario == "verification failed" {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `<Error><Code>SignatureDoesNotMatch</Code><Message>invalid signature</Message></Error>`)
						return
					}
					if scenario == "permissions activating" && attempt == 2 {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `<Error><Code>AccessDenied</Code><Message>no identity-based policy allows s3:ListBucket</Message></Error>`)
						return
					}
					if attempt == 1 {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `<Error><Code>InvalidAccessKeyId</Code><Message>not active yet</Message></Error>`)
						return
					}
					fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
					return
				}
				transfers.Add(1)
				if r.URL.Query().Get("list-type") != "" {
					fmt.Fprint(w, `<ListBucketResult><IsTruncated>false</IsTruncated></ListBucketResult>`)
					return
				}
				if r.Method == http.MethodGet {
					fmt.Fprint(w, "file contents")
				}
			}))
			defer objects.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/me":
					tokenType := "rw"
					if scenario == "read-only" {
						tokenType = "ro"
					}
					fmt.Fprintf(w, `{"organizationId":"org_work","tokenType":%q}`, tokenType)
				case "/buckets":
					fmt.Fprintf(w, `[{"id":"sb_one","name":"uploads","endpoint":%q}]`, objects.URL)
				case "/buckets/sb_one/keys":
					creates.Add(1)
					var body struct{ Name string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name != "Sliplane CLI (work)" || r.Method != http.MethodPost {
						t.Error("incorrect key creation request")
					}
					w.WriteHeader(201)
					fmt.Fprint(w, `{"id":"sk_cli","accessKeyId":"bucket-access","secretAccessKey":"bucket-secret"}`)
				case "/buckets/sb_one/keys/sk_cli":
					deletes.Add(1)
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			command := newCommand(server.URL, server.URL+"/presets")
			inputMaster, input := testTerminal(t)
			outMaster, output := testTerminal(t)
			errMaster, diagnostics := testTerminal(t)
			command.SetIn(input)
			command.SetOut(output)
			command.SetErr(diagnostics)
			answer := "yes\n"
			if scenario == "decline" {
				answer = "n\n"
			}
			if scenario == "enter" {
				answer = "\n"
			}
			if _, err := io.WriteString(inputMaster, answer); err != nil {
				t.Fatal(err)
			}
			var redirected bytes.Buffer
			switch scenario {
			case "piped input":
				command.SetIn(strings.NewReader("yes\n"))
			case "piped output":
				command.SetOut(&redirected)
			case "piped error":
				command.SetErr(&redirected)
			}
			args := []string{"--config", configPath, "--token", "api-test-token", "s3", "ls", "uploads"}
			switch scenario {
			case "upload":
				source := filepath.Join(t.TempDir(), "source.txt")
				if err := os.WriteFile(source, []byte("file contents"), 0600); err != nil {
					t.Fatal(err)
				}
				args[5] = "upload"
				args = append(args, source, "--force")
			case "download":
				args[5] = "download"
				args = append(args, "file.txt", filepath.Join(t.TempDir(), "download.txt"))
			case "json":
				args = append(args, "-o", "json")
			}
			command.SetArgs(args)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := command.ExecuteContext(ctx)
			stdout := readTerminal(t, outMaster, output)
			stderr := readTerminal(t, errMaster, diagnostics)
			success := scenario == "ls" || scenario == "upload" || scenario == "download" || scenario == "permissions activating"
			if success {
				expectedProbes := int32(2)
				if scenario == "permissions activating" {
					expectedProbes = 3
				}
				if err != nil || creates.Load() != 1 || probes.Load() != expectedProbes || transfers.Load() != 1 || !strings.Contains(string(stderr), "[y/N]") || len(stdout) == 0 {
					t.Fatalf("first use: %v; creates=%d transfers=%d; out=%q err=%q", err, creates.Load(), transfers.Load(), stdout, stderr)
				}
				// A fresh command must reuse the stored key without prompting or creating another.
				reused, warnings, err := runProfileCommand(t, server.URL, configPath, "--token", "api-test-token", "s3", "ls", "uploads", "-o", "json")
				if err != nil || strings.TrimSpace(reused) != "[]" || warnings != "" || creates.Load() != 1 {
					t.Fatalf("reuse: %q %q %v", reused, warnings, err)
				}
				saved, _ := config.Load(configPath)
				secret, err := auth.Load(saved.Profiles["work"].S3Credentials["sb_one"])
				raw, _ := os.ReadFile(configPath)
				if err != nil || !strings.Contains(secret, "bucket-secret") || strings.Contains(string(raw), "bucket-secret") {
					t.Fatal("key was not saved securely", err)
				}
			} else if err == nil || transfers.Load() != 0 || len(stdout) != 0 || redirected.Len() != 0 {
				t.Fatalf("unexpected operation: %v out=%q err=%q", err, stdout, stderr)
			}
			expectedError := ""
			switch scenario {
			case "decline", "enter":
				expectedError = "canceled"
			case "json", "piped output", "piped input", "piped error":
				expectedError = "no saved S3 key"
			case "read-only":
				expectedError = "read-write"
			case "keychain failure":
				expectedError = "keychain locked"
			case "verification failed":
				expectedError = "bucket access key was saved, but could not verify access"
			}
			if expectedError != "" && (err == nil || !strings.Contains(err.Error(), expectedError)) {
				t.Fatalf("expected %q error, got %v", expectedError, err)
			}
			if (strings.HasPrefix(scenario, "piped") || scenario == "json" || scenario == "read-only") && stderr != "" {
				t.Fatalf("unexpected prompt: %q", stderr)
			}
			if !success && scenario != "keychain failure" && scenario != "verification failed" && creates.Load() != 0 {
				t.Fatal("created a key without consent")
			}
			if scenario == "keychain failure" && (creates.Load() != 1 || deletes.Load() != 1) {
				t.Fatal("failed setup did not revoke its key")
			}
			if scenario == "verification failed" {
				saved, loadErr := config.Load(configPath)
				_, secretErr := auth.Load(saved.Profiles["work"].S3Credentials["sb_one"])
				if loadErr != nil || secretErr != nil || creates.Load() != 1 || deletes.Load() != 0 || probes.Load() != 1 {
					t.Fatal("verification failure lost or recreated the saved key", loadErr, secretErr)
				}
			}
			if strings.Contains(string(stdout), "[y/N]") || strings.Contains(string(stdout)+string(stderr), "bucket-secret") {
				t.Fatal("prompt polluted stdout or exposed the secret")
			}
		})
	}
}

func TestDownloadProgressOutput(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_S3_ACCESS_KEY_ID", "access")
	t.Setenv("SLIPLANE_S3_SECRET_ACCESS_KEY", "secret")
	objects := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bucket/broken" {
			w.Header().Set("Content-Length", "100")
		}
		fmt.Fprint(w, "download body")
	}))
	defer objects.Close()
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/me" {
			fmt.Fprint(w, `{"organizationId":"org","tokenType":"rw"}`)
			return
		}
		fmt.Fprintf(w, `[{"id":"sb_one","name":"bucket","endpoint":%q}]`, objects.URL)
	}))
	defer control.Close()
	for _, scenario := range []string{"terminal", "json", "redirected output", "redirected error", "broken"} {
		t.Run(scenario, func(t *testing.T) {
			command := newCommand(control.URL, control.URL+"/presets")
			outMaster, output := testTerminal(t)
			errMaster, diagnostics := testTerminal(t)
			command.SetOut(output)
			command.SetErr(diagnostics)
			command.SetIn(strings.NewReader(""))
			var redirected bytes.Buffer
			if scenario == "redirected output" {
				command.SetOut(&redirected)
			}
			if scenario == "redirected error" {
				command.SetErr(&redirected)
			}
			key := "object"
			if scenario == "broken" {
				key = "broken"
			}
			directory := t.TempDir()
			destination := filepath.Join(directory, "download")
			args := []string{"--config", filepath.Join(directory, "config.json"), "--token", "token", "s3", "download", "bucket", key, destination}
			if scenario == "json" {
				args = append(args, "-o", "json")
			}
			command.SetArgs(args)
			err := command.Execute()
			stdout := readTerminal(t, outMaster, output)
			stderr := readTerminal(t, errMaster, diagnostics)
			if scenario == "broken" {
				if err == nil || stdout != "" {
					t.Fatalf("broken download: %q %v", stdout, err)
				}
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("failed download left a destination")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			interactive := scenario == "terminal" || scenario == "broken"
			if strings.Contains(stderr, "Downloading") != interactive {
				t.Fatalf("unexpected progress for %s: %q", scenario, stderr)
			}
			if interactive && !strings.HasSuffix(stderr, "\r\x1b[2K") {
				t.Fatalf("progress was not cleared: %q", stderr)
			}
			if strings.Contains(stdout+redirected.String(), "Downloading") {
				t.Fatal("progress leaked into command output")
			}
			if scenario == "json" && !json.Valid([]byte(stdout)) {
				t.Fatalf("invalid JSON: %q", stdout)
			}
		})
	}
}

func testTerminal(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close(); slave.Close() })
	return master, slave
}

// readTerminal drains through a sentinel before closing; macOS discards unread output on close.
func readTerminal(t *testing.T, master, slave *os.File) string {
	t.Helper()
	if _, err := slave.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}
	output, err := bufio.NewReader(master).ReadString(0)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(output, "\x00")
}
