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
	"github.com/sliplane/cli/internal/api"
	"golang.org/x/term"
)

func TestDeployPromptProcess(t *testing.T) {
	endpoint := os.Getenv("SLIPLANE_TEST_PROMPT_ENDPOINT")
	if endpoint == "" {
		t.Skip("subprocess helper")
	}
	command := newCommand(endpoint, endpoint+"/presets")
	command.SetIn(os.Stdin)
	command.SetOut(os.Stdout)
	command.SetErr(os.Stderr)
	for index, argument := range os.Args {
		if argument == "--" {
			command.SetArgs(os.Args[index+1:])
			break
		}
	}
	if err := Execute(command); err != nil {
		if errors.Is(err, context.Canceled) {
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func TestDeployInteractiveDestination(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("TERM", "xterm-256color")
	type promptReply struct{ title, keys string }
	for _, scenario := range []struct {
		name, projectKeys, serverKeys, projectID, serverID string
		nameKeys, nameFlag, wantName                       string
		createSource                                       []string
		createReplies                                      []promptReply
		omitCreateName                                     bool
	}{
		{name: "arrows", projectKeys: "\x1b[B\x1b[A\r", serverKeys: "\x1b[B\r", projectID: "one", serverID: "two", nameKeys: "\r"},
		{name: "search and rename", projectKeys: "/Zulu\r", serverKeys: "/Alpha\r", projectID: "two", serverID: "one", nameKeys: "\x01\x0bworkflows\r", wantName: "workflows"},
		{name: "explicit name", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", nameFlag: "from-flag", wantName: "from-flag"},
		{name: "empty name", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", nameKeys: "\x01\x0b\r", wantName: "valid-name"},
		{name: "cancel name", projectKeys: "\r", serverKeys: "\r", nameKeys: "\x03"},
		{name: "cancel project", projectKeys: "\x03"},
		{name: "cancel server", projectKeys: "\r", serverKeys: "\x03"},
		{name: "context cancellation"},
		{name: "create image", projectKeys: "/Zulu\r", serverKeys: "/Alpha\r", projectID: "two", serverID: "one", wantName: "custom-api", createSource: []string{"--image", "nginx:1.28"}},
		{name: "create repository", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "custom-api", createSource: []string{"--repo", "https://github.com/acme/api"}},
		{name: "cancel create", projectKeys: "\x03", createSource: []string{"--image", "nginx:1.28"}},
		{name: "create missing image", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "custom-api", createSource: []string{}, createReplies: []promptReply{{"Deploy from", "\r"}, {"Image reference", "nginx:1.28\r"}}},
		{name: "create missing repository", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "custom-api", createSource: []string{}, createReplies: []promptReply{{"Deploy from", "\x1b[B\r"}, {"GitHub repository URL", "https://github.com/acme/api\r"}}},
		{name: "create inferred repository", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "custom-api", createSource: []string{"--branch", "develop"}, createReplies: []promptReply{{"GitHub repository URL", "https://github.com/acme/api\r"}}},
		{name: "create inferred image", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "custom-api", createSource: []string{"--registry-auth", "credential1"}, createReplies: []promptReply{{"Image reference", "nginx:1.28\r"}}},
		{name: "create missing name", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "named-api", omitCreateName: true, createSource: []string{"--image", "nginx:1.28"}, createReplies: []promptReply{{"Service name", "named-api\r"}}},
		{name: "create correct image", projectKeys: "\r", serverKeys: "\r", projectID: "one", serverID: "one", wantName: "custom-api", createSource: []string{}, createReplies: []promptReply{{"Deploy from", "\r"}, {"Image reference", "\r"}, {"--image must not be empty", "nginx:1.28\r"}}},
		{name: "cancel create source", createSource: []string{}, createReplies: []promptReply{{"Deploy from", "\x03"}}},
		{name: "cancel create image", createSource: []string{}, createReplies: []promptReply{{"Deploy from", "\r"}, {"Image reference", "\x03"}}},
		{name: "cancel create name", omitCreateName: true, createSource: []string{}, createReplies: []promptReply{{"Service name", "\x03"}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			created := make(chan api.CreateServiceInput, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/presets":
					fmt.Fprint(w, deployCatalog)
				case "/projects", "/servers":
					fmt.Fprint(w, `[{"id":"two","name":"Zulu"},{"id":"one","name":"Alpha"}]`)
				case "/projects/" + scenario.projectID + "/services":
					var input api.CreateServiceInput
					if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&input) != nil {
						t.Error("invalid service creation")
					}
					created <- input
					fmt.Fprint(w, `{"id":"new","name":"n8n","status":"pending"}`)
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			master, terminal := testTerminal(t)
			if err := pty.Setsize(terminal, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
				t.Fatal(err)
			}
			before, err := term.GetState(int(terminal.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"--config", filepath.Join(t.TempDir(), "config.json"), "--token", "test-token", "deploy", "n8n"}
			if scenario.createSource != nil {
				args = append(args[:4], "service", "create")
				if !scenario.omitCreateName {
					args = append(args, "custom-api")
				}
				args = append(args, scenario.createSource...)
			}
			if scenario.nameFlag != "" {
				args = append(args, "--name", scenario.nameFlag)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			processArgs := append([]string{"-test.run=^TestDeployPromptProcess$", "--"}, args...)
			process := exec.CommandContext(ctx, os.Args[0], processArgs...)
			raceLog := filepath.Join(t.TempDir(), "race")
			process.Env = append(os.Environ(), "SLIPLANE_TEST_PROMPT_ENDPOINT="+server.URL, "GORACE=log_path="+raceLog)
			process.Stdin, process.Stdout, process.Stderr = terminal, terminal, terminal
			if err := process.Start(); err != nil {
				t.Fatal(err)
			}
			frames := make(chan string)
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
			go func() {
				done <- process.Wait()
				close(exited)
			}()
			t.Cleanup(func() { cancel(); <-exited })
			var output strings.Builder
			var projectAnswered, serverAnswered, nameAnswered, nameCorrected bool
			createRepliesAnswered := 0
		wait:
			for {
				select {
				case frame := <-frames:
					output.WriteString(frame)
					keys := ""
					if !projectAnswered && strings.Contains(output.String(), "Choose project") {
						projectAnswered = true
						keys = scenario.projectKeys
						if scenario.name == "context cancellation" {
							if err := process.Process.Signal(os.Interrupt); err != nil {
								t.Fatal(err)
							}
						}
					}
					if !serverAnswered && strings.Contains(output.String(), "Choose server") {
						serverAnswered = true
						keys = scenario.serverKeys
					}
					if scenario.createSource == nil && !nameAnswered && strings.Contains(output.String(), "Service name") {
						nameAnswered = true
						keys = scenario.nameKeys
					}
					if scenario.name == "empty name" && !nameCorrected && strings.Contains(output.String(), "service name must not be empty") {
						nameCorrected = true
						keys = "valid-name\r"
					}
					if createRepliesAnswered < len(scenario.createReplies) {
						reply := scenario.createReplies[createRepliesAnswered]
						if strings.Contains(output.String(), reply.title) {
							createRepliesAnswered++
							keys = reply.keys
						}
					}
					if _, err := io.WriteString(master, keys); err != nil {
						t.Fatal(err)
					}
				case err = <-done:
					break wait
				case <-time.After(12 * time.Second):
					t.Fatalf("menu did not exit: %q", output.String())
				}
			}
			// Cancellation exits with 130, so check race logs even when the exit code is expected.
			raceReports, globError := filepath.Glob(raceLog + ".*")
			if globError != nil {
				t.Fatal(globError)
			}
			for _, report := range raceReports {
				contents, readError := os.ReadFile(report)
				t.Fatalf("child process data race: %s (read error: %v)", contents, readError)
			}
			if scenario.projectID == "" {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) || exitError.ExitCode() != 130 || len(created) != 0 {
					t.Fatalf("cancelled deployment: %v, creations: %d", err, len(created))
				}
			} else {
				if err != nil {
					t.Fatalf("deploy: %v; terminal: %q", err, output.String())
				}
				select {
				case input := <-created:
					if input.ServerID != scenario.serverID {
						t.Fatalf("selected wrong server: %q", input.ServerID)
					}
					if scenario.createSource != nil && input.Deployment.URL != "docker.io/library/nginx:1.28" && input.Deployment.URL != "https://github.com/acme/api" {
						t.Fatalf("incorrect deployment source: %q", input.Deployment.URL)
					}
					if scenario.name == "create inferred repository" && input.Deployment.Branch != "develop" {
						t.Fatal("lost explicit branch")
					}
					if scenario.name == "create inferred image" && input.Deployment.RegistryAuthenticationID != "credential1" {
						t.Fatal("lost registry credentials")
					}
					if scenario.wantName != "" && input.Name != scenario.wantName {
						t.Fatalf("wrong service name: %q", input.Name)
					}
					if scenario.wantName == "" && !strings.HasPrefix(input.Name, "n8n-") {
						t.Fatalf("lost generated name: %q", input.Name)
					}
					if (scenario.nameFlag != "" || scenario.createSource != nil) && nameAnswered {
						t.Fatal("prompted despite explicit --name")
					}
					if scenario.name == "empty name" && !nameCorrected {
						t.Fatal("accepted empty name")
					}
				default:
					t.Fatal("service was not created")
				}
			}
			if createRepliesAnswered != len(scenario.createReplies) {
				t.Fatalf("missing required prompts: answered %d of %d", createRepliesAnswered, len(scenario.createReplies))
			}
			after, err := term.GetState(int(terminal.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("terminal state was not restored: %v", err)
			}
		})
	}
}
