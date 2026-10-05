package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) registryCommand() *cobra.Command {
	command := &cobra.Command{Use: "registry", Aliases: []string{"registries"}, Short: "Manage container registry credentials"}
	command.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List registry credentials", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			credentials, err := client.RegistryCredentials(command.Context())
			if err != nil {
				return err
			}
			sort.Slice(credentials, func(i, j int) bool { return credentials[i].Name < credentials[j].Name })
			rows := [][]string{{"NAME", "TYPE", "USERNAME", "ID"}}
			for _, credential := range credentials {
				rows = append(rows, []string{credential.Name, credential.Type, credential.Username, credential.ID})
			}
			return o.print(command, credentials, rows)
		},
	}, &cobra.Command{Use: "show <name-or-id>", Short: "Show registry credentials without exposing the token", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			credential, err := resolveRegistryCredentials(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			credential, err = client.RegistryCredential(command.Context(), credential.ID)
			if err != nil {
				return err
			}
			return o.printRegistryCredentials(command, credential)
		},
	}, o.registryCreateCommand(), o.registryRenameCommand(), o.registryDeleteCommand())
	return command
}

func (o *options) registryCreateCommand() *cobra.Command {
	var input api.CreateRegistryCredentialsInput
	var tokenStdin bool
	command := &cobra.Command{Use: "create <name>", Short: "Register container registry credentials (requires a read-write token)", Args: validateArguments,
		Long:    "Create registry credentials. Defaults to Docker Hub. Supply the registry token through\nSLIPLANE_REGISTRY_TOKEN, --token-stdin, or the hidden terminal prompt.",
		Example: "  sliplane registry create github --type ghcr --username octocat --token-stdin < token.txt",
		RunE: func(command *cobra.Command, args []string) error {
			input.Name = args[0]
			if strings.TrimSpace(input.Name) == "" {
				return errors.New("credential name must not be empty")
			}
			if !slices.Contains([]string{"dockerhub", "ghcr", "dhi", "generic"}, input.Type) {
				return errors.New("--type must be dockerhub, ghcr, dhi, or generic")
			}
			if input.Username == "" && o.canPrompt(command) {
				if err := runPrompt(command, huh.NewInput().Title("Registry username").Value(&input.Username)); err != nil {
					return err
				}
			}
			if strings.TrimSpace(input.Username) == "" {
				return errors.New("provide --username or rerun in a terminal")
			}
			input.Token = os.Getenv("SLIPLANE_REGISTRY_TOKEN")
			if tokenStdin {
				value, err := io.ReadAll(command.InOrStdin())
				if err != nil {
					return fmt.Errorf("read registry token: %w", err)
				}
				input.Token = strings.TrimRight(string(value), "\r\n")
			}
			if input.Token == "" && !tokenStdin && o.canPrompt(command) {
				if err := runPrompt(command, huh.NewInput().Title("Registry token").EchoMode(huh.EchoModePassword).Value(&input.Token)); err != nil {
					return err
				}
			}
			if strings.TrimSpace(input.Token) == "" {
				return errors.New("provide SLIPLANE_REGISTRY_TOKEN, --token-stdin, or rerun in a terminal")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			credential, err := client.CreateRegistryCredentials(command.Context(), input)
			if err != nil {
				return errors.New(strings.ReplaceAll(err.Error(), input.Token, "[redacted]"))
			}
			return o.printRegistryCredentials(command, credential)
		},
	}
	command.Flags().StringVar(&input.Type, "type", "dockerhub", "Registry type: dockerhub, ghcr, dhi, generic")
	command.Flags().StringVar(&input.Username, "username", "", "Registry username (prompted when omitted)")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Read the registry token from standard input")
	return command
}

func (o *options) registryRenameCommand() *cobra.Command {
	return &cobra.Command{Use: "rename <name-or-id> <new-name>", Short: "Rename registry credentials (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("credential name must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			credential, err := resolveRegistryCredentials(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			credential, err = client.RenameRegistryCredentials(command.Context(), credential.ID, args[1])
			if err != nil {
				return err
			}
			return o.printRegistryCredentials(command, credential)
		},
	}
}

func (o *options) registryDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "delete <name-or-id>", Short: "Delete registry credentials (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			credential, err := resolveRegistryCredentials(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if err := o.confirmResourceDeletion(command, "registry credentials", credential.Name, credential.ID); err != nil {
					return err
				}
			}
			if err := client.DeleteRegistryCredentials(command.Context(), credential.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": credential.ID, "name": credential.Name, "deleted": true}, [][]string{{"Deleted", credential.Name}, {"ID", credential.ID}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm registry credential deletion")
	return command
}

func resolveRegistryCredentials(ctx context.Context, client *api.Client, query string) (api.RegistryCredentials, error) {
	credentials, err := client.RegistryCredentials(ctx)
	if err != nil {
		return api.RegistryCredentials{}, err
	}
	choices := make([]choice, len(credentials))
	for i, credential := range credentials {
		choices[i] = choice{credential.ID, credential.Name}
	}
	index, err := resolve("registry credentials", query, choices)
	if err != nil {
		return api.RegistryCredentials{}, err
	}
	return credentials[index], nil
}

func (o *options) printRegistryCredentials(command *cobra.Command, credential api.RegistryCredentials) error {
	return o.print(command, credential, [][]string{{"Name", credential.Name}, {"ID", credential.ID}, {"Type", credential.Type}, {"Username", credential.Username}, {"Created", formatTimestamp(credential.CreatedAt)}})
}
