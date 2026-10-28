package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateRequiresReleaseBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	command := NewCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{"--config", path, "update"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "not installed from a Sliplane release") || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output %q %q, error %v; want a release build error before loading config", stdout.String(), stderr.String(), err)
	}
}
