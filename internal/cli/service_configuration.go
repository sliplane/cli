package cli

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) serviceEnvCommand() *cobra.Command {
	command := &cobra.Command{Use: "env", Short: "Manage service environment variables"}
	command.AddCommand(&cobra.Command{
		Use: "list <service>", Aliases: []string{"ls"}, Short: "List environment variables (secret values are hidden)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			entries, err := client.ServiceEnvironment(command.Context(), service.ProjectID, service.ID)
			if err != nil {
				return err
			}
			rows := [][]string{{"KEY", "VALUE", "SECRET"}}
			for index := range entries {
				if entries[index].Secret {
					entries[index].Value = ""
				}
				rows = append(rows, []string{entries[index].Key, entries[index].Value, strconv.FormatBool(entries[index].Secret)})
			}
			return o.print(command, entries, rows)
		},
	})
	var secret bool
	set := &cobra.Command{
		Use: "set <service> <KEY=value>", Short: "Set an environment variable; use --secret to hide its value", Args: validateArguments,
		Long:    "Set one environment variable without replacing the others. Pass KEY=value, or KEY to read the local environment. Values are non-secret by default; use --secret for sensitive values.",
		Example: "  sliplane service env set api DATABASE_URL --secret\n  sliplane service env set api LOG_LEVEL=info",
		RunE: func(command *cobra.Command, args []string) error {
			entry, err := parseEnvironmentVariable(args[1])
			if err != nil {
				return err
			}
			entry.Secret = secret
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if err := client.SetServiceEnvironment(command.Context(), service.ProjectID, service.ID, entry); err != nil {
				return err
			}
			return o.print(command, map[string]any{"key": entry.Key, "secret": entry.Secret, "updated": true}, [][]string{{"Updated", entry.Key}, {"Secret", strconv.FormatBool(entry.Secret)}})
		},
	}
	set.Flags().BoolVar(&secret, "secret", false, "Store a secret value")
	command.AddCommand(set, &cobra.Command{
		Use: "delete <service> <key>", Short: "Delete an environment variable", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("environment variable key must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if err := client.DeleteServiceEnvironment(command.Context(), service.ProjectID, service.ID, args[1]); err != nil {
				return err
			}
			return o.print(command, map[string]any{"key": args[1], "deleted": true}, [][]string{{"Deleted", args[1]}})
		},
	})
	return command
}

func (o *options) serviceDomainCommand() *cobra.Command {
	command := &cobra.Command{Use: "domain", Short: "Manage custom domains"}
	command.AddCommand(&cobra.Command{
		Use: "list <service>", Aliases: []string{"ls"}, Short: "List custom domains and verification status", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			service, err = client.Service(command.Context(), service.ProjectID, service.ID)
			if err != nil {
				return err
			}
			var network api.ServiceNetworkDetails
			if err := json.Unmarshal(service.Network, &network); err != nil {
				return err
			}
			domains := append([]api.CustomDomain{}, network.CustomDomains...)
			rows := [][]string{{"DOMAIN", "STATUS", "ID"}}
			for _, domain := range domains {
				rows = append(rows, []string{domain.Domain, domain.Status, domain.ID})
			}
			return o.print(command, domains, rows)
		},
	}, &cobra.Command{
		Use: "add <service> <domain>", Short: "Add a custom domain and start DNS verification", Args: validateArguments,
		Long: "Add a custom domain. Point its CNAME to the service's managed domain or its A record to the server IP. DNS verification can take up to 24 hours; check service domain list for status.",
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" || strings.ContainsAny(args[1], "/: \t\r\n") {
				return errors.New("domain must be a hostname without a scheme, port, or path")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			domain, err := client.AddServiceDomain(command.Context(), service.ProjectID, service.ID, args[1])
			if err != nil {
				return err
			}
			return o.print(command, domain, [][]string{{"Domain", domain.Domain}, {"Status", domain.Status}, {"ID", domain.ID}})
		},
	}, &cobra.Command{
		Use: "remove <service> <domain-or-id>", Short: "Remove a custom domain", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			service, err = client.Service(command.Context(), service.ProjectID, service.ID)
			if err != nil {
				return err
			}
			var network api.ServiceNetworkDetails
			if err := json.Unmarshal(service.Network, &network); err != nil {
				return err
			}
			choices := make([]choice, len(network.CustomDomains))
			for index, domain := range network.CustomDomains {
				choices[index] = choice{domain.ID, domain.Domain}
			}
			index, err := resolve("domain", args[1], choices)
			if err != nil {
				return err
			}
			domain := network.CustomDomains[index]
			if err := client.DeleteServiceDomain(command.Context(), service.ProjectID, service.ID, domain.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": domain.ID, "domain": domain.Domain, "deleted": true}, [][]string{{"Removed", domain.Domain}})
		},
	})
	return command
}
