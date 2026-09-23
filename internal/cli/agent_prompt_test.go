//go:build !windows

// Agent confirmation tests use real terminals to exercise the shared prompt policy.
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAgentInitConfirmation writes only after an affirmative terminal response.
func TestAgentInitConfirmation(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	t.Setenv("CLAUDECODE", "")
	for _, answer := range []string{"yes\n", "n\n", "\n"} {
		t.Run(answer, func(t *testing.T) {
			t.Chdir(t.TempDir())
			command := newCommand("")
			inputMaster, input := testTerminal(t)
			outMaster, output := testTerminal(t)
			errMaster, diagnostics := testTerminal(t)
			command.SetIn(input)
			command.SetOut(output)
			command.SetErr(diagnostics)
			command.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "config.json"), "agent", "init"})
			if _, err := io.WriteString(inputMaster, answer); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := command.ExecuteContext(ctx); err != nil {
				t.Fatal(err)
			}
			stdout := readTerminal(t, outMaster, output)
			stderr := readTerminal(t, errMaster, diagnostics)
			if !strings.Contains(stderr, "AGENTS.md? [y/N]") {
				t.Fatalf("missing confirmation: %q", stderr)
			}
			_, err := os.Stat("AGENTS.md")
			if answer == "yes\n" {
				if err != nil || !strings.Contains(stdout, "created") {
					t.Fatalf("confirmed init failed: %q %v", stdout, err)
				}
				return
			}
			if !errors.Is(err, os.ErrNotExist) || !strings.Contains(stdout, "cancelled") {
				t.Fatalf("declined init wrote a file: %q %v", stdout, err)
			}
		})
	}
}
