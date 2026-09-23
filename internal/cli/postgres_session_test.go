//go:build !windows

// psql subprocess tests verify private credentials, stream forwarding, and cleanup.
package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

// TestPostgresCommands exercises discovery and native command arguments without needing a database.
func TestPostgresCommands(t *testing.T) {
	t.Setenv("SLIPLANE_PROFILE", "")
	workspace := t.TempDir()
	psql := filepath.Join(workspace, "psql")
	if err := os.WriteFile(psql, []byte("#!/bin/sh\n[ -f \"$PGPASSFILE\" ] || exit 90\nprintf '<%s>\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", workspace+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("unrestricted database triggered a write")
		}
		switch r.URL.Path {
		case "/postgres":
			fmt.Fprint(w, `[{"id":"pg_main","name":"main"}]`)
		case "/postgres/pg_main":
			fmt.Fprint(w, `{"id":"pg_main","name":"main","connection":{"host":"127.0.0.1","port":5432,"database":"app","user":"owner","password":"secret"},"ipAllowList":[{"source":"0.0.0.0/0"}]}`)
		default:
			t.Errorf("unexpected API request: %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, name := range []string{"main", "pg_main"} {
		stdout, stderr, err := runCommand(t, server.URL, "postgres", "connect", name)
		if err != nil || stdout != "<--no-password>\n" || stderr != "" {
			t.Fatalf("connect: %q %q %v", stdout, stderr, err)
		}
		stdout, stderr, err = runCommand(t, server.URL, "postgres", "query", name, "SELECT 'literal $value';")
		if err != nil || stdout != "<--no-password>\n<--no-psqlrc>\n<--set>\n<ON_ERROR_STOP=1>\n<--command>\n<SELECT 'literal $value';>\n" || stderr != "" {
			t.Fatalf("query: %q %q %v", stdout, stderr, err)
		}
	}
}

// TestPSQLInteractiveInterrupt sends terminal Ctrl-C to both CLI and child, then exits the surviving shell.
func TestPSQLInteractiveInterrupt(t *testing.T) {
	if os.Getenv("SLIPLANE_TEST_PSQL_HELPER") == "1" {
		command := &cobra.Command{RunE: func(command *cobra.Command, _ []string) error {
			connection := api.PostgresConnection{Host: "localhost", Port: 5432, Database: "app", User: "owner", Password: "secret"}
			return runPSQL(command, os.Getenv("SLIPLANE_TEST_PSQL_PATH"), connection, "127.0.0.1", nil)
		}}
		command.SetArgs([]string{})
		if err := Execute(command); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	workspace := t.TempDir()
	psql := filepath.Join(workspace, "psql")
	script := "#!/bin/sh\ntrap 'echo canceled' INT\necho ready\nwhile :; do\n  read line\n  if [ \"$line\" = quit ]; then exit 0; fi\ndone\n"
	if err := os.WriteFile(psql, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPSQLInteractiveInterrupt$")
	process.Env = append(os.Environ(), "SLIPLANE_TEST_PSQL_HELPER=1", "SLIPLANE_TEST_PSQL_PATH="+psql)
	terminal, err := pty.Start(process)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	lines := make(chan string, 10)
	go func() {
		scanner := bufio.NewScanner(terminal)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	for _, expected := range []string{"ready", "canceled"} {
		found := false
		for !found {
			select {
			case <-ctx.Done():
				t.Fatalf("did not receive %q: %v", expected, ctx.Err())
			case line, open := <-lines:
				if !open {
					t.Fatalf("shell exited before %q", expected)
				}
				found = strings.Contains(line, expected)
			}
		}
		if expected == "ready" {
			if _, err := terminal.Write([]byte{3}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := io.WriteString(terminal, "quit\n"); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("Ctrl-C canceled the parent instead of leaving psql open: %v", err)
	}
}

// TestPSQLSession keeps credentials out of process arguments and removes the passfile on every exit.
func TestPSQLSession(t *testing.T) {
	for _, scenario := range []string{"connect", "query", "failure", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := t.TempDir()
			trace := filepath.Join(workspace, "trace")
			t.Setenv("PSQL_TEST_TRACE", trace)
			t.Setenv("PSQL_TEST_SCENARIO", scenario)
			t.Setenv("PGPASSWORD", "wrong-password")
			t.Setenv("PGSERVICE", "wrong-database")
			t.Setenv("PGHOSTADDR", "192.0.2.99")
			t.Setenv("PGSSLMODE", "disable")
			script := `#!/bin/sh
if [ -n "$PGPASSWORD" ] || [ -n "$PGSERVICE" ]; then exit 90; fi
if [ "$PGHOST" != db.example.test ] || [ "$PGHOSTADDR" != 203.0.113.10 ]; then exit 91; fi
if [ "$PGPORT" != 5432 ] || [ "$PGDATABASE" != app ] || [ "$PGUSER" != owner ]; then exit 92; fi
if [ "$PGSSLMODE" != verify-full ] || [ "$PGSSLROOTCERT" != system ]; then exit 93; fi
printf '%s\n' "$PGPASSFILE" > "$PSQL_TEST_TRACE"
ls -l "$PGPASSFILE" >> "$PSQL_TEST_TRACE"
cat "$PGPASSFILE" >> "$PSQL_TEST_TRACE"
printf '<%s>\n' "$@" >> "$PSQL_TEST_TRACE"
case "$PSQL_TEST_SCENARIO" in
  connect) cat ;;
  query) printf 'query result\n' ;;
  failure) printf 'SQL failed\n' >&2; exit 3 ;;
  cancel) printf 'started\n'; exec /bin/sleep 30 ;;
esac
`
			psql := filepath.Join(workspace, "psql")
			if err := os.WriteFile(psql, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			command := &cobra.Command{}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			command.SetContext(ctx)
			command.SetIn(strings.NewReader("interactive input\n"))
			var stdout, stderr bytes.Buffer
			command.SetOut(&stdout)
			command.SetErr(&stderr)
			if scenario == "cancel" {
				command.SetOut(&sshCancelWriter{cancel: cancel})
			}
			arguments := []string{}
			if scenario != "connect" {
				arguments = []string{"--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--command", "SELECT 'quoted SQL';"}
			}
			connection := api.PostgresConnection{Host: "db.example.test", Port: 5432, Database: "app", User: "owner", Password: `secret:p\ass`}
			err := runPSQL(command, psql, connection, "203.0.113.10", arguments)
			switch scenario {
			case "connect":
				if err != nil || stdout.String() != "interactive input\n" {
					t.Fatalf("interactive streams: %q %v", stdout.String(), err)
				}
			case "query":
				if err != nil || stdout.String() != "query result\n" {
					t.Fatalf("query: %q %v", stdout.String(), err)
				}
			case "failure":
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) || exitError.ExitCode() != 3 || !strings.Contains(stderr.String(), "SQL failed") {
					t.Fatalf("SQL error status: %v", err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation: %v", err)
				}
			}
			data, readErr := os.ReadFile(trace)
			if readErr != nil {
				t.Fatal(readErr)
			}
			lines := strings.Split(string(data), "\n")
			if len(lines) < 4 || !strings.HasPrefix(lines[1], "-rw-------") || lines[2] != `db.example.test:5432:app:owner:secret\:p\\ass` || lines[3] != "<--no-password>" {
				t.Fatal("password file was missing, incorrectly escaped, or not private")
			}
			if _, err := os.Stat(filepath.Dir(lines[0])); !os.IsNotExist(err) {
				t.Fatal("temporary PostgreSQL credentials remained after exit")
			}
			if scenario != "connect" && !strings.Contains(string(data), "<--command>\n<SELECT 'quoted SQL';>") {
				t.Fatal("SQL was not passed as a single argument")
			}
			if strings.Contains(stdout.String()+stderr.String(), "secret") {
				t.Fatal("session exposed connection credentials")
			}
		})
	}
}
