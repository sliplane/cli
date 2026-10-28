package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sliplane/cli/internal/api"
	"github.com/sliplane/cli/internal/auth"
	"github.com/sliplane/cli/internal/config"
	"github.com/zalando/go-keyring"
)

func TestS3KeyLifecycle(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInit()
	configPath := filepath.Join(t.TempDir(), "config.json")
	preferences := config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{
		"work": {OrganizationID: "org_work", CredentialID: "work-key", S3Credentials: map[string]string{"sb_one": "s3_saved", "sb_other": "s3_other"}},
	}}
	if err := config.Save(configPath, preferences); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save("s3_saved", `{"keyId":"sk_cli","accessKeyId":"cli-access","secretAccessKey":"cli-secret"}`); err != nil {
		t.Fatal(err)
	}
	keys := map[string]api.CreatedBucketKey{
		"sk_cli": {BucketKey: api.BucketKey{ID: "sk_cli", Name: "Sliplane CLI (work)", AccessKeyID: "cli-access"}, SecretAccessKey: "cli-secret"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me":
			fmt.Fprint(w, `{"organizationId":"org_work","tokenType":"rw"}`)
		case "/buckets":
			fmt.Fprint(w, `[{"id":"sb_one","name":"uploads"}]`)
		case "/buckets/sb_one/keys":
			if r.Method == http.MethodGet {
				listed := []api.CreatedBucketKey{}
				for _, key := range keys {
					listed = append(listed, key)
				}
				// Even an overbroad API response must not leak secrets through key listings.
				json.NewEncoder(w).Encode(listed)
				return
			}
			if r.Method != http.MethodPost {
				t.Error("unexpected key operation", r.Method)
			}
			var input struct{ Name string }
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Name != "ci" {
				t.Error("incorrect key name", input.Name, err)
			}
			key := api.CreatedBucketKey{BucketKey: api.BucketKey{ID: "sk_ci", Name: input.Name, AccessKeyID: "ci-access"}, SecretAccessKey: "ci-secret"}
			keys[key.ID] = key
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(key)
		case "/buckets/sb_one/keys/sk_ci", "/buckets/sb_one/keys/sk_cli":
			if r.Method != http.MethodDelete {
				t.Error("revocation must DELETE")
			}
			delete(keys, strings.TrimPrefix(r.URL.Path, "/buckets/sb_one/keys/"))
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	output, stderr, err := runProfileCommand(t, server.URL, configPath, "--token", "api-token", "s3", "key", "create", "uploads", "ci", "-o", "json")
	var created api.CreatedBucketKey
	if err != nil || stderr != "" || json.Unmarshal([]byte(output), &created) != nil || created.SecretAccessKey != "ci-secret" {
		t.Fatalf("creation did not return one-time credentials: %q %q %v", output, stderr, err)
	}
	for _, format := range []string{"text", "json"} {
		output, stderr, err = runProfileCommand(t, server.URL, configPath, "--token", "api-token", "s3", "key", "list", "uploads", "-o", format)
		if err != nil || stderr != "" || !strings.Contains(output, "ci-access") || strings.Contains(output, "secret") {
			t.Fatalf("key list: %q %q %v", output, stderr, err)
		}
	}
	for _, query := range []string{"ci", "sk_cli"} {
		output, _, err = runProfileCommand(t, server.URL, configPath, "--token", "api-token", "s3", "key", "delete", "uploads", query, "--yes", "-o", "json")
		if err != nil || !json.Valid([]byte(output)) || !strings.Contains(output, `"deleted":true`) {
			t.Fatalf("delete: %q %v", output, err)
		}
		saved, err := config.Load(configPath)
		if err != nil {
			t.Fatal(err)
		}
		_, secretErr := auth.Load("s3_saved")
		if query == "ci" && (saved.Profiles["work"].S3Credentials["sb_one"] != "s3_saved" || secretErr != nil) {
			t.Fatal("creating or deleting another key changed the saved CLI key")
		}
		if query == "sk_cli" && (saved.Profiles["work"].S3Credentials["sb_one"] != "" || secretErr == nil) {
			t.Fatal("deleted CLI key still saved")
		}
		if saved.Profiles["work"].S3Credentials["sb_other"] != "s3_other" {
			t.Fatal("deletion changed another bucket's credentials")
		}
	}
	output, _, err = runProfileCommand(t, server.URL, configPath, "--token", "api-token", "s3", "key", "list", "uploads", "-o", "json")
	if err != nil || strings.TrimSpace(output) != "[]" {
		t.Fatalf("empty list: %q %v", output, err)
	}
}

func TestS3KeyPermissions(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	for _, test := range []struct {
		action, tokenType string
		wantError         bool
	}{
		{"list", "ro", false}, {"create", "ro", true}, {"delete", "ro", true}, {"create", "rw", false},
	} {
		t.Run(test.action+"/"+test.tokenType, func(t *testing.T) {
			keyring.MockInitWithError(fmt.Errorf("keychain must not be used"))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/me":
					fmt.Fprintf(w, `{"organizationId":"org","tokenType":%q}`, test.tokenType)
				case "/buckets":
					fmt.Fprint(w, `[{"id":"sb_one","name":"uploads"}]`)
				case "/buckets/sb_one/keys":
					if test.action == "list" {
						fmt.Fprint(w, `null`)
						return
					}
					if test.wantError {
						t.Error("read-only command reached key operations")
						w.WriteHeader(http.StatusForbidden)
						return
					}
					fmt.Fprint(w, `{"id":"sk_ci","name":"ci","accessKeyId":"access","secretAccessKey":"secret"}`)
				default:
					t.Error("unexpected request", r.URL.Path)
				}
			}))
			defer server.Close()
			args := []string{"s3", "key", test.action, "uploads"}
			if test.action != "list" {
				args = append(args, "ci")
			}
			output, stderr, err := runCommand(t, server.URL, args...)
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "read-write") || output != "" {
					t.Fatalf("read-only mutation: %q %v", output, err)
				}
				return
			}
			if err != nil || stderr != "" || output == "" {
				t.Fatalf("key command without a saved profile: %q %q %v", output, stderr, err)
			}
		})
	}
}

func TestS3KeyDeleteResolution(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	keyring.MockInit()
	configPath := filepath.Join(t.TempDir(), "config.json")
	preferences := config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{
		"work": {OrganizationID: "org", CredentialID: "work-key", S3Credentials: map[string]string{"sb_one": "s3_saved"}},
	}}
	if err := config.Save(configPath, preferences); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save("s3_saved", `{"keyId":"sk_b","accessKeyId":"access","secretAccessKey":"secret"}`); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/me":
			fmt.Fprint(w, `{"organizationId":"org","tokenType":"rw"}`)
		case "/buckets":
			fmt.Fprint(w, `[{"id":"sb_one","name":"uploads"}]`)
		case "/buckets/sb_one/keys":
			fmt.Fprint(w, `[{"id":"sk_a","name":"ci"},{"id":"sk_b","name":"ci"}]`)
		case "/buckets/sb_one/keys/sk_b":
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"message":"cannot revoke key"}`)
		default:
			t.Error("unexpected deletion", r.URL.Path)
		}
	}))
	defer server.Close()
	for _, test := range []struct{ query, want string }{{"ci", "ambiguous"}, {"missing", "not found"}, {"sk_b", "cannot revoke key"}} {
		output, _, err := runProfileCommand(t, server.URL, configPath, "--token", "api-token", "s3", "key", "delete", "uploads", test.query, "--yes")
		if err == nil || !strings.Contains(err.Error(), test.want) || output != "" {
			t.Fatalf("delete %q: %q %v", test.query, output, err)
		}
	}
	output, _, err := runProfileCommand(t, server.URL, configPath, "--token", "api-token", "s3", "key", "delete", "uploads", "sk_a")
	if err == nil || !strings.Contains(err.Error(), "--yes") || output != "" {
		t.Fatalf("unconfirmed delete: %q %v", output, err)
	}
	saved, err := config.Load(configPath)
	if err != nil || saved.Profiles["work"].S3Credentials["sb_one"] != "s3_saved" {
		t.Fatal("failed deletion changed saved credentials", err)
	}
	if _, err := auth.Load("s3_saved"); err != nil {
		t.Fatal("failed deletion removed the saved secret", err)
	}
}

func TestS3KeyCommandHelp(t *testing.T) {
	command, _, err := NewCommand().Find([]string{"s3"})
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range command.Commands() {
		if child.Name() == "connect" || child.Name() == "disconnect" {
			t.Fatal("connection command is still registered")
		}
	}
	output, _, err := runCommand(t, "http://127.0.0.1:1", "s3", "key", "create", "uploads")
	if err == nil || output != "" || !strings.Contains(err.Error(), "missing required argument: name") {
		t.Fatalf("missing key name: %q %v", output, err)
	}
}
