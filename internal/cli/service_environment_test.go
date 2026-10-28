package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sliplane/cli/internal/api"
)

func TestServiceEnvironment(t *testing.T) {
	t.Setenv("LOCAL", "from-local")
	t.Setenv("EMPTY_LOCAL", "")
	first := filepath.Join(t.TempDir(), "first.env")
	second := filepath.Join(t.TempDir(), "second.env")
	if err := os.WriteFile(first, []byte("# comment\r\n\r\n KEY=first\r\nEMPTY=\r\nLOCAL\r\nEMPTY_LOCAL\r\nLITERAL=\"$LOCAL $(echo never)\"\r\nEQUALS=a=b\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("KEY=second\nFILE_ONLY=second\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := serviceEnvironment([]string{first, second}, []string{"KEY=third", "KEY=fourth", "SECRET=secret"}, []string{"KEY=public", "PORT=3000"})
	want := []api.EnvironmentVariable{
		{Key: "KEY", Value: "public", Secret: false},
		{Key: "EMPTY", Value: "", Secret: true},
		{Key: "LOCAL", Value: "from-local", Secret: true},
		{Key: "EMPTY_LOCAL", Value: "", Secret: true},
		{Key: "LITERAL", Value: `"$LOCAL $(echo never)"`, Secret: true},
		{Key: "EQUALS", Value: "a=b", Secret: true},
		{Key: "FILE_ONLY", Value: "second", Secret: true},
		{Key: "SECRET", Value: "secret", Secret: true},
		{Key: "PORT", Value: "3000", Secret: false},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("environment = %#v, error = %v; want %#v", got, err, want)
	}
}

func TestServiceEnvironmentErrors(t *testing.T) {
	t.Setenv("SLIPLANE_CREATE_MISSING", "")
	if err := os.Unsetenv("SLIPLANE_CREATE_MISSING"); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"=hidden-secret", "BAD KEY=hidden-secret", "BAD\x00KEY=hidden-secret", "SLIPLANE_CREATE_MISSING"} {
		if _, err := parseEnvironmentVariable(entry); err == nil || strings.Contains(err.Error(), "hidden-secret") {
			t.Fatalf("unsafe or missing environment error: %v", err)
		}
	}
	filename := filepath.Join(t.TempDir(), "bad.env")
	if err := os.WriteFile(filename, []byte("# comment\nBAD KEY=hidden-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := serviceEnvironment([]string{filename}, nil, nil); err == nil || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), "hidden-secret") {
		t.Fatalf("file error: %v", err)
	}
}
