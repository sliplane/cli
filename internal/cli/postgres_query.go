package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/gohxs/readline"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func (o *options) postgresQueryCommand() *cobra.Command {
	var file string
	var timeout time.Duration
	command := &cobra.Command{
		Use: "query <name-or-id> [sql]", Short: "Execute SQL from an argument, file, or stdin", Args: validateArguments,
		Long:    "Execute quoted SQL, --file SQL, or piped stdin without installing psql.\nUse --file - to explicitly read stdin. SQL arguments and --file cannot be combined.\n--timeout limits the whole command, including input and connection setup; 0 disables it.\nIf your IP is blocked, a terminal session asks before saving a persistent allowlist rule.\nRedirected commands never prompt; allow your IP in the dashboard first.",
		Example: "  sliplane postgres query main 'SELECT now();'\n  sliplane postgres query main --file script.sql --timeout 30s\n  cat script.sql | sliplane postgres query main --timeout 1m",
		RunE: func(command *cobra.Command, args []string) (err error) {
			if o.output != "text" {
				return errors.New("PostgreSQL commands require --output text")
			}
			if timeout < 0 {
				return errors.New("--timeout must not be negative")
			}
			parent := command.Context()
			ctx := parent
			if timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(parent, timeout)
				defer cancel()
			}
			command.SetContext(ctx)
			defer func() {
				command.SetContext(parent)
				if ctx.Err() != nil {
					err = ctx.Err()
				}
				if errors.Is(err, context.DeadlineExceeded) {
					err = fmt.Errorf("PostgreSQL query timed out: %w", err)
				}
			}()
			sql, err := readPostgresQuery(command, args, file)
			if err != nil {
				return err
			}
			if strings.TrimSpace(sql) == "" {
				return errors.New("SQL must not be empty")
			}
			dsn, err := o.postgresDSN(command, args[0])
			if err != nil {
				return err
			}
			return o.runPostgres(command, dsn, sql)
		},
	}
	command.Flags().StringVarP(&file, "file", "f", "", "Read SQL from a file (- for stdin)")
	command.Flags().DurationVar(&timeout, "timeout", 0, "Maximum total duration, e.g. 30s or 2m (0 disables)")
	return command
}

// readPostgresQuery rejects ambiguous input and lets cancellation interrupt a blocked stdin read.
func readPostgresQuery(command *cobra.Command, args []string, file string) (string, error) {
	if command.Flags().Changed("file") {
		if len(args) == 2 {
			return "", errors.New("provide either a SQL argument or --file, not both")
		}
		if file == "" {
			return "", errors.New("--file requires a path or - for stdin")
		}
	}
	if len(args) == 2 {
		return args[1], nil
	}
	input := command.InOrStdin()
	source := "stdin"
	if file != "" && file != "-" {
		opened, err := os.Open(file)
		if err != nil {
			return "", fmt.Errorf("read SQL file %q: %w", file, err)
		}
		defer opened.Close()
		input, source = opened, file
	} else if terminal, ok := input.(*os.File); ok && term.IsTerminal(int(terminal.Fd())) {
		return "", errors.New("provide a SQL argument, --file script.sql, or pipe SQL to stdin")
	}
	reader := readline.NewCancelableStdin(input)
	defer reader.Close()
	stop := context.AfterFunc(command.Context(), func() { _ = reader.Close() })
	defer stop()
	content, err := io.ReadAll(reader)
	if command.Context().Err() != nil {
		return "", command.Context().Err()
	}
	if err != nil {
		return "", fmt.Errorf("read SQL from %s: %w", source, err)
	}
	return string(content), nil
}
