//go:build !windows

package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeleteConfirmation(t *testing.T) {
	for _, resource := range []struct {
		command, target, deletePath string
		responses                   map[string]string
	}{
		{"service", `"staging" (s1) in project "p1"`, "/projects/p1/services/s1", map[string]string{
			"/projects":             `[{"id":"p1","name":"preview"}]`,
			"/projects/p1/services": `[{"id":"s1","name":"staging","projectId":"p1"}]`,
		}},
		{"postgres", `"staging" (pg_target)`, "/postgres/pg_target", map[string]string{
			"/postgres":           `[{"id":"pg_target","name":"staging"}]`,
			"/postgres/pg_target": `{"id":"pg_target","name":"staging"}`,
		}},
	} {
		for _, scenario := range []string{"approve", "decline", "default no", "piped answer", "json"} {
			t.Run(resource.command+"/"+scenario, func(t *testing.T) {
				deletes := 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, exists := resource.responses[r.URL.Path]
					switch {
					case r.Method == "GET" && exists:
						fmt.Fprint(w, body)
					case r.Method == "DELETE" && r.URL.Path == resource.deletePath:
						deletes++
						w.WriteHeader(http.StatusNoContent)
					default:
						t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
						http.NotFound(w, r)
					}
				}))
				defer server.Close()
				inputMaster, input := testTerminal(t)
				outputMaster, output := testTerminal(t)
				errorMaster, diagnostics := testTerminal(t)
				command := newCommand(server.URL, server.URL+"/presets")
				command.SetIn(input)
				command.SetOut(output)
				command.SetErr(diagnostics)
				args := []string{"--config", filepath.Join(t.TempDir(), "config.json"), "--token", "test-token", resource.command, "delete", "staging"}
				answer := "yes\n"
				switch scenario {
				case "decline":
					answer = "no\n"
				case "default no":
					answer = "\n"
				case "piped answer":
					command.SetIn(strings.NewReader("yes\n"))
				case "json":
					args = append(args, "--output", "json")
				}
				if _, err := io.WriteString(inputMaster, answer); err != nil {
					t.Fatal(err)
				}
				command.SetArgs(args)
				ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
				defer cancel()
				err := command.ExecuteContext(ctx)
				stdout := readTerminal(t, outputMaster, output)
				stderr := readTerminal(t, errorMaster, diagnostics)
				if scenario == "approve" {
					if err != nil || deletes != 1 || !strings.Contains(stdout, "Deleted") {
						t.Fatalf("deletion: %q %v (%d requests)", stdout, err, deletes)
					}
				} else if err == nil || deletes != 0 || stdout != "" {
					t.Fatalf("deleted without consent: %q %v (%d requests)", stdout, err, deletes)
				}
				if scenario == "piped answer" || scenario == "json" {
					if stderr != "" {
						t.Fatalf("prompted in noninteractive mode: %q", stderr)
					}
				} else if !strings.Contains(stderr, resource.target) || !strings.Contains(stderr, "[y/N]") {
					t.Fatalf("confirmation did not identify its target: %q", stderr)
				}
			})
		}
	}
}
