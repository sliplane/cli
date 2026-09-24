// Argument errors explain the repair without running commands or contaminating stdout.
package cli

import (
	"strings"
	"testing"
)

// TestArgumentErrors checks missing values, extra values, aliases, and nested command usage.
func TestArgumentErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		message string
		usage   string
	}{
		{"bucket name", []string{"s3", "create"}, "missing required argument: bucket name", "sliplane s3 create <bucket-name>"},
		{"upload inputs", []string{"s3", "upload"}, "missing required arguments: bucket, file", "sliplane s3 upload <bucket> <file> [key]"},
		{"upload file", []string{"s3", "upload", "assets"}, "missing required argument: file", "sliplane s3 upload <bucket> <file> [key]"},
		{"download key", []string{"s3", "download", "assets"}, "missing required argument: key", "sliplane s3 download <bucket> <key> [file]"},
		{"config value", []string{"config", "set", "output"}, "missing required argument: value", "sliplane config set <project|output> <value>"},
		{"nested logs", []string{"service", "logs"}, "missing required argument: name or id", "sliplane service logs <name-or-id>"},
		{"deploy shortcut", []string{"deploy"}, "missing required argument: preset", "sliplane deploy <preset>"},
		{"extra name", []string{"s3", "create", "my", "bucket"}, "too many arguments; if a value contains spaces, wrap it in quotes", "sliplane s3 create <bucket-name>"},
		{"extra optional value", []string{"s3", "ls", "assets", "prefix", "extra"}, "too many arguments", "sliplane s3 list [bucket] [prefix]"},
		{"no positional values", []string{"auth", "login", "secret-token"}, "this command does not take positional arguments", "sliplane auth login"},
	} {
		t.Run(test.name, func(t *testing.T) {
			stdout, stderr, err := runCommand(t, "http://127.0.0.1:1", test.args...)
			if err == nil {
				t.Fatal("expected an argument error")
			}
			for _, want := range []string{test.message, "Usage:\n  " + test.usage, "--help' for details."} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
			if strings.Contains(err.Error(), "secret-token") {
				t.Error("error echoed a supplied credential")
			}
			if stdout != "" || stderr != "" {
				t.Fatalf("argument validation wrote output: %q %q", stdout, stderr)
			}
			if test.name == "bucket name" && !strings.Contains(err.Error(), "Examples:\n  sliplane s3 create my-bucket") {
				t.Fatalf("missing bucket creation example: %v", err)
			}
		})
	}
}

// TestValidArgumentCounts preserves optional arguments without executing side effects.
func TestValidArgumentCounts(t *testing.T) {
	for _, args := range [][]string{
		{"s3", "create", "my-bucket"},
		{"s3", "list"}, {"s3", "list", "assets"}, {"s3", "ls", "assets", "prefix"},
		{"s3", "upload", "assets", "photo.jpg"}, {"s3", "upload", "assets", "photo.jpg", "images/photo.jpg"},
		{"s3", "download", "assets", "photo.jpg"}, {"s3", "download", "assets", "photo.jpg", "local.jpg"},
		{"config", "set", "output", "json"}, {"service", "list"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			command, remaining, err := NewCommand().Find(args)
			if err != nil {
				t.Fatal(err)
			}
			if err := command.ValidateArgs(remaining); err != nil {
				t.Fatal(err)
			}
		})
	}
}
