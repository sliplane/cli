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

func TestPostgresQueryInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script.sql")
	script := "-- a comment\nSELECT $$one;two$$;\nSELECT 2\n"
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, stdin, want, failure string
		args, flags                []string
	}{
		{name: "argument ignores stdin", args: []string{"main", "SELECT 1"}, stdin: "SELECT 2", want: "SELECT 1"},
		{name: "file ignores stdin", args: []string{"main"}, flags: []string{"--file", path}, stdin: "SELECT 2", want: script},
		{name: "implicit stdin", args: []string{"main"}, stdin: script, want: script},
		{name: "explicit stdin", args: []string{"main"}, flags: []string{"-f", "-"}, stdin: script, want: script},
		{name: "missing file", args: []string{"main"}, flags: []string{"--file", path + ".missing"}, failure: "read SQL file"},
		{name: "directory", args: []string{"main"}, flags: []string{"--file", filepath.Dir(path)}, failure: "read SQL from"},
		{name: "conflicting sources", args: []string{"main", "SELECT 1"}, flags: []string{"--file", path}, failure: "either a SQL argument or --file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := (&options{}).postgresQueryCommand()
			command.SetContext(t.Context())
			command.SetIn(strings.NewReader(test.stdin))
			if err := command.Flags().Parse(test.flags); err != nil {
				t.Fatal(err)
			}
			file, _ := command.Flags().GetString("file")
			got, err := readPostgresQuery(command, test.args, file)
			if test.failure != "" {
				if err == nil || !strings.Contains(err.Error(), test.failure) {
					t.Fatalf("expected %q, got %v", test.failure, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("SQL = %q, error = %v", got, err)
			}
		})
	}
}

func TestPostgresQueryEmptyInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.sql")
	if err := os.WriteFile(path, []byte(" \n\t"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, flags := range [][]string{nil, {"--file", "-"}, {"--file", path}} {
		command := (&options{output: "text"}).postgresQueryCommand()
		command.SetIn(strings.NewReader(" \n\t"))
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		command.SetArgs(append([]string{"main"}, flags...))
		if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "SQL must not be empty") {
			t.Fatalf("%v: %v", flags, err)
		}
	}
}

func TestPostgresQueryInputTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	command := (&options{output: "text"}).postgresQueryCommand()
	command.SetIn(reader)
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"main", "--timeout", "50ms"})
	start := time.Now()
	err := command.ExecuteContext(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "query timed out") || time.Since(start) > time.Second {
		t.Fatalf("timeout while reading stdin: %v", err)
	}
}
