// Postgres commands inspect databases and open usql sessions without printing credentials.
package cli

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

// postgresCommand lists and inspects databases in the selected organization.
func (o *options) postgresCommand() *cobra.Command {
	var all bool
	command := &cobra.Command{Use: "postgres", Short: "Manage and connect to PostgreSQL databases"}
	list := &cobra.Command{
		Use: "list", Short: "List PostgreSQL databases", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			databases, err := client.PostgresDatabases(command.Context())
			if err != nil {
				return err
			}
			sort.Slice(databases, func(i, j int) bool {
				if databases[i].Name != databases[j].Name {
					return databases[i].Name < databases[j].Name
				}
				return databases[i].ID < databases[j].ID
			})
			rows := [][]string{{"NAME", "STATUS", "REGION", "VERSION", "DISK", "DATABASE"}}
			if all {
				rows[0] = append(rows[0], "ID", "USER", "HOST", "PORT", "ALLOWED IPS", "CREATED BY", "CREATED")
			}
			for _, database := range databases {
				row := []string{database.Name, database.Status, database.Region, strconv.Itoa(database.Version), strconv.Itoa(database.DiskSizeGB) + " GB", database.DatabaseName}
				if all {
					host, port := "", ""
					if database.Connection != nil {
						host = database.Connection.Host
						port = strconv.Itoa(database.Connection.Port)
					}
					sources := []string{}
					for _, entry := range database.IPAllowList {
						sources = append(sources, entry.Source)
					}
					row = append(row, database.ID, database.DatabaseUser, host, port, strings.Join(sources, ", "), database.CreatedByUserID, formatTimestamp(database.CreatedAt))
				}
				rows = append(rows, row)
			}
			return o.print(command, databases, rows)
		},
	}
	list.Flags().BoolVarP(&all, "all", "a", false, "Show additional columns (does not change which resources are listed)")
	command.AddCommand(list, &cobra.Command{
		Use: "show <name-or-id>", Short: "Show a PostgreSQL database", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			database, err := resolvePostgres(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			return o.printPostgres(command, database)
		},
	})
	command.AddCommand(o.postgresConnectCommand(), o.postgresQueryCommand(),
		o.postgresCreateCommand(), o.postgresUpdateCommand(), o.postgresDeleteCommand(), o.postgresRotatePasswordCommand(),
		o.postgresLifecycleCommand("restart", "Restart a database"),
		o.postgresLifecycleCommand("pause", "Pause a database"),
		o.postgresLifecycleCommand("unpause", "Resume a paused database"),
	)
	return command
}

// resolvePostgres shares exact name and ID lookup between inspection and SQL commands.
func resolvePostgres(ctx context.Context, client *api.Client, name string) (api.PostgresDatabase, error) {
	databases, err := client.PostgresDatabases(ctx)
	if err != nil {
		return api.PostgresDatabase{}, err
	}
	choices := make([]choice, len(databases))
	for index, database := range databases {
		choices[index] = choice{database.ID, database.Name}
	}
	index, err := resolve("database", name, choices)
	if err != nil {
		return api.PostgresDatabase{}, err
	}
	return client.PostgresDatabase(ctx, databases[index].ID)
}

// printPostgres shows database state while excluding passwords from both output formats.
func (o *options) printPostgres(command *cobra.Command, database api.PostgresDatabase) error {
	rows := [][]string{{"Name", database.Name}, {"Status", database.Status}, {"Region", database.Region}, {"Version", strconv.Itoa(database.Version)}, {"Disk", strconv.Itoa(database.DiskSizeGB) + " GB"}, {"Database", database.DatabaseName}, {"User", database.DatabaseUser}}
	if database.Connection != nil {
		rows = append(rows, []string{"Host", database.Connection.Host}, []string{"Port", strconv.Itoa(database.Connection.Port)})
	}
	sources := []string{}
	for _, entry := range database.IPAllowList {
		sources = append(sources, entry.Source)
	}
	rows = append(rows, []string{"Allowed IPs", strings.Join(sources, ", ")}, []string{"Created", formatTimestamp(database.CreatedAt)}, []string{"ID", database.ID})
	return o.print(command, database, rows)
}
