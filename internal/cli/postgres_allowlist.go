// Allowlist edits preserve unrelated rules; an empty list blocks all public access.
package cli

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/sliplane/cli/internal/api"
	"github.com/spf13/cobra"
)

func (o *options) postgresAllowlistCommand() *cobra.Command {
	command := &cobra.Command{Use: "allowlist", Aliases: []string{"allowlists"}, Short: "Manage persistent PostgreSQL IP access rules"}
	command.AddCommand(&cobra.Command{Use: "list <name-or-id>", Aliases: []string{"ls"}, Short: "List allowed IP ranges", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			_, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			return o.printPostgresAllowlist(command, database.IPAllowList)
		},
	}, o.postgresAllowlistAddCommand(), o.postgresAllowlistRemoveCommand(), o.postgresAllowlistClearCommand())
	return command
}

func (o *options) postgresAllowlistAddCommand() *cobra.Command {
	var description string
	command := &cobra.Command{Use: "add <name-or-id> <ip-or-cidr>", Short: "Allow an IP address or range (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			source, err := postgresCIDR(args[1])
			if err != nil {
				return err
			}
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			entries := slices.Clone(database.IPAllowList)
			for index, entry := range entries {
				existing, err := postgresCIDR(entry.Source)
				if err != nil || existing != source {
					continue
				}
				if !command.Flags().Changed("description") {
					return o.printPostgresAllowlist(command, entries)
				}
				entries[index].Description = description
				if err := client.SetPostgresAllowList(command.Context(), database.ID, entries); err != nil {
					return err
				}
				return o.printPostgresAllowlist(command, entries)
			}
			entries = append(entries, api.PostgresIPRange{Source: source, Description: description})
			if err := client.SetPostgresAllowList(command.Context(), database.ID, entries); err != nil {
				return err
			}
			return o.printPostgresAllowlist(command, entries)
		},
	}
	command.Flags().StringVar(&description, "description", "", "Optional label for this rule")
	return command
}

func (o *options) postgresAllowlistRemoveCommand() *cobra.Command {
	return &cobra.Command{Use: "remove <name-or-id> <ip-cidr-or-rule-id>", Short: "Remove an allowed IP range (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			source, _ := postgresCIDR(args[1])
			entries := make([]api.PostgresIPRange, 0, len(database.IPAllowList))
			for _, entry := range database.IPAllowList {
				existing, _ := postgresCIDR(entry.Source)
				if entry.ID == args[1] || source != "" && existing == source {
					continue
				}
				entries = append(entries, entry)
			}
			if len(entries) == len(database.IPAllowList) {
				return fmt.Errorf("allowlist rule %q not found", args[1])
			}
			if err := client.SetPostgresAllowList(command.Context(), database.ID, entries); err != nil {
				return err
			}
			return o.printPostgresAllowlist(command, entries)
		},
	}
}

func (o *options) postgresAllowlistClearCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "clear <name-or-id>", Short: "Block all public database access (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, database, err := o.postgresAccess(command, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if !o.canPrompt(command) {
					return errors.New("clearing the allowlist requires confirmation; use --yes or rerun in a terminal")
				}
				approved, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), fmt.Sprintf("Block all public access to database %q?", database.Name))
				if err != nil {
					return err
				}
				if !approved {
					return errors.New("allowlist clearing canceled")
				}
			}
			entries := []api.PostgresIPRange{}
			if err := client.SetPostgresAllowList(command.Context(), database.ID, entries); err != nil {
				return err
			}
			return o.printPostgresAllowlist(command, entries)
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm blocking all public access")
	return command
}

// postgresCIDR accepts individual addresses as host routes and normalizes network ranges.
func postgresCIDR(value string) (string, error) {
	if address, err := netip.ParseAddr(value); err == nil && address.Zone() == "" {
		address = address.Unmap()
		return netip.PrefixFrom(address, address.BitLen()).String(), nil
	}
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		return "", fmt.Errorf("invalid IP address or CIDR %q", value)
	}
	return prefix.Masked().String(), nil
}

func (o *options) printPostgresAllowlist(command *cobra.Command, entries []api.PostgresIPRange) error {
	if entries == nil {
		entries = []api.PostgresIPRange{}
	}
	rows := [][]string{{"SOURCE", "DESCRIPTION", "ID"}}
	for _, entry := range entries {
		rows = append(rows, []string{entry.Source, entry.Description, entry.ID})
	}
	return o.print(command, entries, rows)
}
