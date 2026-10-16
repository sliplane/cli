//go:build !windows

package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
)

// Run the terminal UI in its own process, as the other interactive prompt tests do.
func TestLoginPromptProcess(t *testing.T) {
	endpoint := os.Getenv("SLIPLANE_TEST_LOGIN_ENDPOINT")
	if endpoint == "" {
		t.Skip("subprocess helper")
	}
	keyring.MockInit()
	for _, ref := range []string{"work-key", "s3_work"} {
		if err := auth.Save(ref, "previous-secret"); err != nil {
			t.Fatal(err)
		}
	}
	command := newCommand(endpoint, endpoint)
	command.SetIn(os.Stdin)
	command.SetOut(os.Stdout)
	command.SetErr(os.Stderr)
	for index, arg := range os.Args {
		if arg == "--" {
			command.SetArgs(os.Args[index+1:])
			break
		}
	}
	err := Execute(command)
	saved, loadErr := config.Load(os.Getenv("SLIPLANE_TEST_LOGIN_CONFIG"))
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	state := map[string]bool{}
	for _, ref := range []string{"work-key", "s3_work"} {
		value, err := auth.Load(ref)
		state[ref] = err == nil && value == "previous-secret"
	}
	for name, profile := range saved.Profiles {
		value, err := auth.Load(profile.CredentialID)
		state["new:"+name] = err == nil && value == "new-secret"
	}
	data, _ := json.Marshal(state)
	if err := os.WriteFile(os.Getenv("SLIPLANE_TEST_LOGIN_STATE"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestLoginTeamSwitch(t *testing.T) {
	t.Setenv("SLIPLANE_TOKEN", "")
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("TERM", "xterm-256color")
	type reply struct{ prompt, keys string }
	for _, scenario := range []struct {
		name    string
		replies []reply
		outcome string
	}{
		{"overwrite", []reply{{"Switch teams", "\r"}}, "overwrite"},
		{"create", []reply{{"Switch teams", "\x1b[B\r"}, {"New profile name", "personal\r"}}, "create"},
		{"cancel", []reply{{"Switch teams", "\x1b[B\x1b[B\r"}}, "cancel"},
		{"interrupt", []reply{{"Switch teams", "\x03"}}, "cancel"},
		{"cancel name", []reply{{"Switch teams", "\x1b[B\r"}, {"New profile name", "\x03"}}, "cancel"},
		{"duplicate name", []reply{{"Switch teams", "\x1b[B\r"}, {"New profile name", "work\r"}, {"already exists", "\x01\x0bpersonal\r"}}, "create"},
		{"invalid name", []reply{{"Switch teams", "\x1b[B\r"}, {"New profile name", "bad name\r"}, {"only letters", "\x01\x0bpersonal\r"}}, "create"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			keyring.MockInit()
			path := filepath.Join(t.TempDir(), "config.json")
			original := config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{
				"work": {OrganizationID: "org_work", CredentialID: "work-key", Project: "production", S3Credentials: map[string]string{"bucket": "s3_work"}},
			}}
			if err := config.Save(path, original); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/me" {
					t.Errorf("unexpected request %s", r.URL.Path)
				}
				fmt.Fprint(w, `{"organizationId":"org_personal","authType":"oauth","tokenType":"rw","availableOrganizations":[{"id":"org_work","name":"Work Team"},{"id":"org_personal","name":"Personal Team"}]}`)
			}))
			defer server.Close()
			master, terminal := testTerminal(t)
			if err := pty.Setsize(terminal, &pty.Winsize{Rows: 30, Cols: 120}); err != nil {
				t.Fatal(err)
			}
			before, err := term.GetState(int(terminal.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			statePath := filepath.Join(t.TempDir(), "keychain-state.json")
			raceLog := filepath.Join(t.TempDir(), "race")
			process := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLoginPromptProcess$", "--", "--config", path, "auth", "login", "--token", "new-secret")
			process.Env = append(os.Environ(), "SLIPLANE_TEST_LOGIN_ENDPOINT="+server.URL, "SLIPLANE_TEST_LOGIN_CONFIG="+path, "SLIPLANE_TEST_LOGIN_STATE="+statePath, "GORACE=log_path="+raceLog)
			process.Stdin, process.Stdout, process.Stderr = terminal, terminal, terminal
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			frames := make(chan string, 64)
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, err := master.Read(buffer)
					if err != nil {
						return
					}
					select {
					case frames <- string(buffer[:n]):
					case <-ctx.Done():
						return
					}
				}
			}()
			done := make(chan error, 1)
			exited := make(chan struct{})
			go func() { done <- process.Wait(); close(exited) }()
			t.Cleanup(func() { cancel(); <-exited })
			var output strings.Builder
			answered := 0
		wait:
			for {
				select {
				case frame := <-frames:
					output.WriteString(frame)
					if answered < len(scenario.replies) && strings.Contains(output.String(), scenario.replies[answered].prompt) {
						if _, err := io.WriteString(master, scenario.replies[answered].keys); err != nil {
							t.Fatal(err)
						}
						answered++
					}
				case err = <-done:
					break wait
				}
			}
			reports, globErr := filepath.Glob(raceLog + ".*")
			if globErr != nil {
				t.Fatal(globErr)
			}
			for _, report := range reports {
				data, _ := os.ReadFile(report)
				t.Fatalf("child race: %s", data)
			}
			data, readErr := os.ReadFile(statePath)
			if readErr != nil {
				t.Fatalf("missing credential results: %v; process %v; %s", readErr, err, output.String())
			}
			var keychainState map[string]bool
			if err := json.Unmarshal(data, &keychainState); err != nil {
				t.Fatal(err)
			}
			if answered != len(scenario.replies) {
				t.Fatalf("missing prompts: %s; %v", output.String(), err)
			}
			if !strings.Contains(output.String(), "Work Team") || !strings.Contains(output.String(), "Personal Team") || strings.Contains(output.String(), "org_work") || strings.Contains(output.String(), "org_personal") {
				t.Fatalf("prompt should show team names: %s", output.String())
			}
			if strings.Contains(output.String(), "new-secret") || strings.Contains(output.String(), "previous-secret") {
				t.Fatal("prompt exposed credentials")
			}
			after, stateErr := term.GetState(int(terminal.Fd()))
			if stateErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("terminal state not restored", stateErr)
			}
			saved, loadErr := config.Load(path)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if scenario.outcome == "cancel" {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 || !reflect.DeepEqual(saved, original) {
					t.Fatalf("cancel changed config: %+v, %v", saved, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				name := "work"
				if scenario.outcome == "create" {
					name = "personal"
				}
				profile := saved.Profiles[name]
				if saved.ActiveProfile != name || profile.OrganizationID != "org_personal" || profile.OrganizationName != "Personal Team" || profile.Project != "" || len(profile.S3Credentials) != 0 {
					t.Fatalf("incorrect saved profile: %+v", saved)
				}
				if !keychainState["new:"+name] {
					t.Fatal("new token not saved")
				}
				if scenario.outcome == "create" {
					work := saved.Profiles["work"]
					if work.Project != "production" || work.CredentialID != "work-key" || work.S3Credentials["bucket"] != "s3_work" || work.OrganizationName != "Work Team" {
						t.Fatalf("old profile changed: %+v", work)
					}
				}
			}
			for _, ref := range []string{"work-key", "s3_work"} {
				if keychainState[ref] != (scenario.outcome != "overwrite") {
					t.Fatalf("incorrect credential cleanup for %s: %+v", ref, keychainState)
				}
			}
			if scenario.outcome != "cancel" {
				stdout, _, err := runProfileCommand(t, server.URL, path, "profile", "list")
				if err != nil || !strings.Contains(stdout, "Personal Team") || strings.Contains(stdout, "org_personal") {
					t.Fatalf("profile names: %s %v", stdout, err)
				}
				stdout, _, err = runProfileCommand(t, server.URL, path, "auth", "status", "--token", "new-secret")
				if err != nil || !strings.Contains(stdout, "Personal Team") || strings.Contains(stdout, "org_personal") {
					t.Fatalf("status names: %s %v", stdout, err)
				}
			}
		})
	}
}

func TestLoginTeamMismatchNoninteractive(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("SLIPLANE_TOKEN", "")
	keyring.MockInit()
	path := filepath.Join(t.TempDir(), "config.json")
	original := config.Config{ActiveProfile: "work", Profiles: map[string]config.Profile{"work": {OrganizationID: "org_work", OrganizationName: "Work Team", CredentialID: "work-key", Project: "production"}}}
	if err := config.Save(path, original); err != nil {
		t.Fatal(err)
	}
	if err := auth.Save("work-key", "old-token"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"organizationId":"org_personal","availableOrganizations":[{"id":"org_personal","name":"Personal Team"}]}`)
	}))
	defer server.Close()
	for _, format := range []string{"text", "json"} {
		output, stderr, err := runProfileCommand(t, server.URL, path, "auth", "login", "--token", "new-token", "-o", format)
		if err == nil || output != "" || stderr != "" || !strings.Contains(err.Error(), "--profile") || !strings.Contains(err.Error(), "Work Team") || !strings.Contains(err.Error(), "Personal Team") || strings.Contains(err.Error(), "org_") {
			t.Fatalf("noninteractive mismatch: %q %q %v", output, stderr, err)
		}
		saved, err := config.Load(path)
		if err != nil || !reflect.DeepEqual(saved, original) {
			t.Fatal("noninteractive login changed config", err)
		}
		if token, err := auth.Load("work-key"); err != nil || token != "old-token" {
			t.Fatal("noninteractive login changed token", err)
		}
	}
	output, _, err := runProfileCommand(t, server.URL, path, "auth", "login", "--profile", "personal", "--token", "new-token", "-o", "json")
	if err != nil || !json.Valid([]byte(output)) {
		t.Fatalf("explicit new profile: %s %v", output, err)
	}
}
