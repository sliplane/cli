// Version reporting must work offline even when local configuration cannot be loaded.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sliplane-cli/internal/buildinfo"
)

// TestVersionWithoutConfig checks output and validation without credentials or valid preferences.
func TestVersionWithoutConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SLIPLANE_PROFILE", "missing-profile")
	t.Setenv("SLIPLANE_TOKEN", "")
	for _, test := range []struct {
		name      string
		args      []string
		wantError string
	}{
		{name: "text", args: []string{"version"}},
		{name: "json", args: []string{"version", "-o", "json"}},
		{name: "invalid format", args: []string{"version", "-o", "yaml"}, wantError: "--output must be text or json"},
		{name: "extra argument", args: []string{"version", "extra"}, wantError: "this command does not take positional arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := NewCommand()
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			command.SetArgs(append([]string{"--config", path}, test.args...))
			err := command.Execute()
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) || stdout.Len() != 0 {
					t.Fatalf("output %q, error %v; want %q with no output", stdout.String(), err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.name == "json" {
				var metadata map[string]string
				if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
					t.Fatal(err)
				}
				if metadata["version"] != buildinfo.Version || metadata["commit"] != commit || len(metadata) != 2 {
					t.Fatalf("unexpected metadata: %v", metadata)
				}
				return
			}
			if want := "sliplane " + buildinfo.Version + " (commit " + commit + ")\n"; stdout.String() != want {
				t.Fatalf("output %q; want %q", stdout.String(), want)
			}
		})
	}
}
