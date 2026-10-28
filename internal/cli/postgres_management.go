package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

func (o *options) postgresCreateCommand() *cobra.Command {
	var input api.CreatePostgresInput
	command := &cobra.Command{
		Use: "create <name>", Short: "Create a PostgreSQL database (requires a read-write token)", Args: validateArguments,
		Long:    "Create a managed PostgreSQL database. The default compute tier is base.\nThe API chooses the default disk size, PostgreSQL version, database name, and user when omitted.\nNew databases use the API's default allowlist, which allows all IPv4 and IPv6 addresses.\nProvisioning continues after this command returns; use postgres show to check status.",
		Example: "  sliplane postgres create main\n  sliplane postgres create main --instance-type medium --disk-size 50 --region ger",
		RunE: func(command *cobra.Command, args []string) error {
			input.Name = args[0]
			if strings.TrimSpace(input.Name) == "" {
				return errors.New("database name must not be empty")
			}
			for _, name := range []string{"region", "instance-type", "database-name", "database-user"} {
				value, _ := command.Flags().GetString(name)
				if (name == "region" || name == "instance-type" || command.Flags().Changed(name)) && strings.TrimSpace(value) == "" {
					return fmt.Errorf("--%s must not be empty", name)
				}
			}
			for _, name := range []string{"disk-size", "version"} {
				value, _ := command.Flags().GetInt(name)
				if command.Flags().Changed(name) && value <= 0 {
					return fmt.Errorf("--%s must be greater than zero", name)
				}
			}
			if input.BillingCycle != "hourly" && input.BillingCycle != "monthly" && input.BillingCycle != "yearly" {
				return errors.New("--billing-cycle must be hourly, monthly, or yearly")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			database, err := client.CreatePostgres(command.Context(), input)
			if err != nil {
				return err
			}
			return o.printPostgres(command, database)
		},
	}
	command.Flags().StringVar(&input.Region, "region", "ger", "Database region (e.g. ger, fin, us-east, us-west, sin, conf)")
	command.Flags().StringVar(&input.InstanceType, "instance-type", "base", "Compute tier (e.g. starter, base, medium, dedicated-base)")
	command.Flags().StringVar(&input.BillingCycle, "billing-cycle", "hourly", "Billing cycle: hourly, monthly, yearly")
	command.Flags().IntVar(&input.DiskSizeGB, "disk-size", 0, "Disk size in GB (omit for the tier's default)")
	command.Flags().IntVar(&input.Version, "version", 0, "PostgreSQL major version (omit for the API default)")
	command.Flags().StringVar(&input.DatabaseName, "database-name", "", "SQL database name (omit for the API default)")
	command.Flags().StringVar(&input.DatabaseUser, "database-user", "", "SQL owner role (omit for the API default)")
	return command
}

func (o *options) postgresUpdateCommand() *cobra.Command {
	var name, instanceType string
	var diskSize int
	command := &cobra.Command{
		Use: "update <name-or-id>", Short: "Rename or resize a database (requires a read-write token)", Args: validateArguments,
		Long:    "Update the display name, compute tier, or disk size. Only supplied fields are changed.\nDisk size must increase; the API validates available tiers and supported compute upgrades.\nUse postgres show to check the status of a resize.",
		Example: "  sliplane postgres update main --name production\n  sliplane postgres update main --instance-type medium --disk-size 50",
		RunE: func(command *cobra.Command, args []string) error {
			input := api.UpdatePostgresInput{}
			if command.Flags().Changed("name") {
				if strings.TrimSpace(name) == "" {
					return errors.New("--name must not be empty")
				}
				input.Name = &name
			}
			if command.Flags().Changed("instance-type") {
				if strings.TrimSpace(instanceType) == "" {
					return errors.New("--instance-type must not be empty")
				}
				input.InstanceType = &instanceType
			}
			if command.Flags().Changed("disk-size") {
				if diskSize <= 0 {
					return errors.New("--disk-size must be greater than zero")
				}
				input.DiskSizeGB = &diskSize
			}
			if input.Name == nil && input.InstanceType == nil && input.DiskSizeGB == nil {
				return errors.New("provide --name, --instance-type, or --disk-size")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			database, err := resolvePostgres(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if input.DiskSizeGB != nil && diskSize <= database.DiskSizeGB {
				return fmt.Errorf("--disk-size must be greater than the current %d GB", database.DiskSizeGB)
			}
			database, err = client.UpdatePostgres(command.Context(), database.ID, input)
			if err != nil {
				return err
			}
			return o.printPostgres(command, database)
		},
	}
	command.Flags().StringVar(&name, "name", "", "New display name")
	command.Flags().StringVar(&instanceType, "instance-type", "", "New compute tier")
	command.Flags().IntVar(&diskSize, "disk-size", 0, "New, larger disk size in GB")
	return command
}

func (o *options) postgresDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use: "delete <name-or-id>", Short: "Permanently delete a database (requires a read-write token)", Args: validateArguments,
		Long:    "Permanently delete a PostgreSQL database and its data.\nRequires confirmation in a terminal, or --yes for noninteractive use.",
		Example: "  sliplane postgres delete staging\n  sliplane postgres delete pg_example --yes",
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			database, err := resolvePostgres(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if !o.canPrompt(command) {
					return errors.New("database deletion requires confirmation; use --yes or rerun in a terminal")
				}
				question := fmt.Sprintf("Permanently delete database %q (%s) and its data?", database.Name, terminalText(database.ID))
				approved, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), question)
				if err != nil {
					return err
				}
				if !approved {
					return errors.New("database deletion canceled")
				}
			}
			if err := client.DeletePostgres(command.Context(), database.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": database.ID, "name": database.Name, "deleted": true}, [][]string{{"Deleted", database.Name}, {"ID", database.ID}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm permanent database deletion")
	return command
}

func (o *options) postgresLifecycleCommand(action, description string) *cobra.Command {
	return &cobra.Command{
		Use: action + " <name-or-id>", Short: description + " (requires a read-write token)", Args: validateArguments,
		Long: description + ". The operation may continue after this command returns.\nUse postgres show to check the current status.",
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			database, err := resolvePostgres(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			database, err = client.PostgresAction(command.Context(), database.ID, action)
			if err != nil {
				return err
			}
			return o.printPostgres(command, database)
		},
	}
}

func (o *options) postgresRotatePasswordCommand() *cobra.Command {
	return &cobra.Command{
		Use: "rotate-password <name-or-id>", Short: "Rotate the owner password and print it (requires a read-write token)", Args: validateArguments,
		Long: "Generate a new password for the managed PostgreSQL owner role and print it.\nUpdate applications that use the old password. The CLI uses current credentials automatically.",
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			database, err := resolvePostgres(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			database, err = client.PostgresAction(command.Context(), database.ID, "credentials/rotate")
			if err != nil {
				return err
			}
			if database.Connection == nil || database.Connection.Password == "" {
				return errors.New("password rotated, but the API did not return the new credentials; retrieve them from the dashboard")
			}
			connection := database.Connection
			credentials := map[string]any{"id": database.ID, "name": database.Name, "host": connection.Host, "port": connection.Port, "database": connection.Database, "user": connection.User, "password": connection.Password}
			return o.print(command, credentials, [][]string{{"Name", database.Name}, {"ID", database.ID}, {"Host", connection.Host}, {"Port", strconv.Itoa(connection.Port)}, {"Database", connection.Database}, {"User", connection.User}, {"Password", connection.Password}})
		},
	}
}
