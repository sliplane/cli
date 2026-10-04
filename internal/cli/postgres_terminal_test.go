//go:build !windows

package cli

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/spf13/cobra"
)

func TestPostgresTerminal(t *testing.T) {
	if os.Getenv("SLIPLANE_TEST_USQL_HELPER") == "1" {
		command := &cobra.Command{RunE: func(command *cobra.Command, _ []string) error {
			return (&options{output: "text"}).runPostgres(command, os.Getenv("SLIPLANE_TEST_USQL_DSN"), "")
		}}
		command.SetArgs([]string{})
		if err := Execute(command); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	connection := testPostgresServer(t)
	dsn := postgresConnectionURL(connection, "127.0.0.1")
	database, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPostgresTerminal$")
	process.Env = append(os.Environ(), "SLIPLANE_TEST_USQL_HELPER=1", "SLIPLANE_TEST_USQL_DSN="+dsn, "PATH=", "TERM=dumb", "NO_COLOR=1")
	terminal, err := pty.Start(process)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	chunks := make(chan string, 32)
	go func() {
		defer close(chunks)
		buffer := make([]byte, 4096)
		for {
			n, err := terminal.Read(buffer)
			if n > 0 {
				select {
				case chunks <- string(buffer[:n]):
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	var transcript strings.Builder
	waitFor := func(expected string) {
		t.Helper()
		for !strings.Contains(transcript.String(), expected) {
			select {
			case <-ctx.Done():
				t.Fatalf("waiting for %q: %s", expected, transcript.String())
			case chunk, open := <-chunks:
				if !open {
					t.Fatalf("shell exited waiting for %q: %s", expected, transcript.String())
				}
				transcript.WriteString(chunk)
			}
		}
	}
	waitFor("Type \"help\" for help.")
	if _, err := io.WriteString(terminal, "SELECT pg_sleep(30);\n"); err != nil {
		t.Fatal(err)
	}
	// Wait until the backend is executing before generating the terminal's SIGINT.
	for {
		var active bool
		err := database.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE query = 'SELECT pg_sleep(30);' AND state = 'active')").Scan(&active)
		if err != nil {
			t.Fatal(err)
		}
		if active {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	if _, err := terminal.Write([]byte{3}); err != nil {
		t.Fatal(err)
	}
	waitFor("canceling statement due to user request")
	if _, err := io.WriteString(terminal, "SELECT 'still-alive';\n"); err != nil {
		t.Fatal(err)
	}
	waitFor("(1 row)")
	if _, err := io.WriteString(terminal, "\\conninfo\n\\q\n"); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatalf("shell did not survive Ctrl-C: %v; %s", err, transcript.String())
	}
	for chunk := range chunks {
		transcript.WriteString(chunk)
	}
	if !strings.Contains(transcript.String(), "still-alive") || strings.Contains(transcript.String(), connection.Password) {
		t.Fatalf("unexpected shell output: %s", transcript.String())
	}
}
