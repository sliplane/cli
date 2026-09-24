//go:build !windows

// Database deletion prompts must name the resolved target and require an explicit affirmative answer.
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

// TestPostgresDeleteConfirmation verifies real terminal consent and refuses piped answers.
func TestPostgresDeleteConfirmation(t *testing.T) {
	for _, scenario := range []string{"approve", "decline", "default no", "piped answer", "json"} {
		t.Run(scenario, func(t *testing.T) {
			deletes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Path == "/postgres":
					fmt.Fprint(w, `[{"id":"pg_target","name":"staging"}]`)
				case r.Method == "GET" && r.URL.Path == "/postgres/pg_target":
					fmt.Fprint(w, `{"id":"pg_target","name":"staging"}`)
				case r.Method == "DELETE" && r.URL.Path == "/postgres/pg_target":
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
			args := []string{"--config", filepath.Join(t.TempDir(), "config.json"), "--token", "test-token", "postgres", "delete", "staging"}
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
			} else if !strings.Contains(stderr, `"staging" (pg_target)`) || !strings.Contains(stderr, "[y/N]") {
				t.Fatalf("confirmation did not identify its target: %q", stderr)
			}
		})
	}
}
