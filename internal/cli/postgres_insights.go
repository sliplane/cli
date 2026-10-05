// PostgreSQL diagnostics show concise tables while preserving complete snapshots in JSON.
package cli

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) postgresRestoreCommand() *cobra.Command {
	var at string
	command := &cobra.Command{
		Use: "restore <name-or-id>", Short: "Restore into a new database (requires a read-write token)", Args: validateArguments,
		Long:    "Create a new database with the source's configuration and restore its data.\nDefaults to the latest recoverable timestamp. Use --at to select an earlier point.\nThe source is unchanged. Provisioning continues after this command returns.",
		Example: "  sliplane postgres restore main\n  sliplane postgres restore main --at 2026-05-25T10:00:00Z",
		RunE: func(command *cobra.Command, args []string) error {
			var timestamp time.Time
			var err error
			if at != "" {
				timestamp, err = time.Parse(time.RFC3339Nano, at)
				if err != nil {
					return errors.New("--at must be an RFC3339 timestamp")
				}
			}
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			window, err := client.PostgresRestoreWindow(command.Context(), database.ID)
			if err != nil {
				return err
			}
			if at == "" {
				timestamp = window.Max
			}
			if timestamp.Before(window.Min) || timestamp.After(window.Max) {
				return fmt.Errorf("--at must be between %s and %s", window.Min.Format(time.RFC3339Nano), window.Max.Format(time.RFC3339Nano))
			}
			restored, err := client.RestorePostgres(command.Context(), database.ID, timestamp)
			if err != nil {
				return err
			}
			return o.printPostgres(command, restored)
		},
	}
	command.Flags().StringVar(&at, "at", "", "Restore timestamp in RFC3339 (default: latest recoverable point)")
	return command
}

func (o *options) postgresRestoreWindowCommand() *cobra.Command {
	return &cobra.Command{Use: "restore-window <name-or-id>", Short: "Show the earliest and latest recoverable timestamps", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			window, err := client.PostgresRestoreWindow(command.Context(), database.ID)
			if err != nil {
				return err
			}
			return o.print(command, window, [][]string{{"Earliest", window.Min.Format(time.RFC3339Nano)}, {"Latest", window.Max.Format(time.RFC3339Nano)}})
		},
	}
}

func (o *options) postgresQueryStatsCommand(kind, description string) *cobra.Command {
	return &cobra.Command{Use: kind + " <name-or-id>", Aliases: []string{strings.TrimSuffix(kind, "queries") + "query"}, Short: description, Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			queries, err := client.PostgresQueries(command.Context(), database.ID, kind)
			if err != nil {
				return err
			}
			sort.SliceStable(queries, func(i, j int) bool { return queries[i].Rank < queries[j].Rank })
			rows := [][]string{{"RANK", "CALLS", "MEAN MS", "MAX MS", "TOTAL MS", "QUERY"}}
			for _, query := range queries {
				rows = append(rows, []string{strconv.Itoa(query.Rank), strconv.FormatInt(query.Calls, 10), fmt.Sprintf("%.2f", query.MeanExecMS), fmt.Sprintf("%.2f", query.MaxExecMS), fmt.Sprintf("%.2f", query.TotalExecMS), query.Query})
			}
			return o.print(command, queries, rows)
		},
	}
}

func (o *options) postgresConnectionsCommand() *cobra.Command {
	return &cobra.Command{Use: "active-connections <name-or-id>", Aliases: []string{"active-connection"}, Short: "Show the latest connection snapshot", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			connections, err := client.PostgresActiveConnections(command.Context(), database.ID)
			if err != nil {
				return err
			}
			rows := [][]string{{"PID", "USER", "DATABASE", "CLIENT", "STATE", "WAIT", "QUERY"}}
			for _, connection := range connections {
				rows = append(rows, []string{strconv.Itoa(connection.PID), connection.User, connection.Database, connection.ClientAddr, connection.State, connection.WaitEvent, connection.Query})
			}
			return o.print(command, connections, rows)
		},
	}
}

func (o *options) postgresSizesCommand() *cobra.Command {
	return &cobra.Command{Use: "relation-sizes <name-or-id>", Aliases: []string{"relation-size"}, Short: "Show table and index sizes, largest first", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			sizes, err := client.PostgresRelationSizes(command.Context(), database.ID)
			if err != nil {
				return err
			}
			sort.SliceStable(sizes, func(i, j int) bool { return sizes[i].TotalBytes > sizes[j].TotalBytes })
			rows := [][]string{{"SCHEMA", "TABLE", "TABLE SIZE", "INDEX SIZE", "TOTAL SIZE"}}
			for _, size := range sizes {
				rows = append(rows, []string{size.SchemaName, size.TableName, formatBytes(size.TableBytes), formatBytes(size.IndexBytes), formatBytes(size.TotalBytes)})
			}
			return o.print(command, sizes, rows)
		},
	}
}

func (o *options) postgresLogsCommand() *cobra.Command {
	var since, until string
	var limit int
	command := &cobra.Command{Use: "logs <name-or-id>", Aliases: []string{"log"}, Short: "Read recent PostgreSQL server logs (retained for 24 hours)", Args: validateArguments,
		Example: "  sliplane postgres logs main\n  sliplane postgres logs main --since 30m --limit 1000 -o json",
		RunE: func(command *cobra.Command, args []string) error {
			if limit < 1 || limit > 1000 {
				return errors.New("--limit must be between 1 and 1000")
			}
			from, err := parseSince(since, time.Now())
			if err != nil {
				return err
			}
			var to *time.Time
			if until != "" {
				timestamp, err := time.Parse(time.RFC3339Nano, until)
				if err != nil {
					return errors.New("--until must be an RFC3339 timestamp")
				}
				to = &timestamp
			}
			if from != nil && to != nil && from.After(*to) {
				return errors.New("--since must be before --until")
			}
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			logs, err := client.PostgresLogs(command.Context(), database.ID, from, to, limit)
			if err != nil {
				return err
			}
			sort.SliceStable(logs, func(i, j int) bool { return logs[i].CreatedAt.Before(logs[j].CreatedAt) })
			if o.output == "json" {
				return writeJSON(command.OutOrStdout(), logs)
			}
			for _, line := range logs {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s  %s  %s\n", formatLocalTime(line.CreatedAt), terminalText(line.Severity), terminalText(line.Message)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&since, "since", "", "Start time as RFC3339 or lookback duration (e.g. 30m)")
	command.Flags().StringVar(&until, "until", "", "End time as RFC3339 (default: now)")
	command.Flags().IntVar(&limit, "limit", 100, "Maximum recent log lines (1–1000)")
	return command
}

// postgresAccess resolves a database using the same exact-name or ID rules as other commands.
func (o *options) postgresAccess(command *cobra.Command, name string) (*api.Client, api.PostgresDatabase, error) {
	client, _, err := o.client(command)
	if err != nil {
		return nil, api.PostgresDatabase{}, err
	}
	database, err := resolvePostgres(command.Context(), client, name)
	return client, database, err
}
