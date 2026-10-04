package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) sshKeyCommand() *cobra.Command {
	command := &cobra.Command{Use: "ssh-key", Short: "Manage persistent SSH public keys"}
	command.AddCommand(o.sshKeyListCommand(), o.sshKeyRegisterCommand(), o.sshKeyDeleteCommand(), o.sshKeyAccessCommand("attach"), o.sshKeyAccessCommand("detach"))
	return command
}

func (o *options) sshKeyListCommand() *cobra.Command {
	return &cobra.Command{Use: "list [service]", Aliases: []string{"ls"}, Short: "List your SSH keys, optionally only those attached to a service", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			if len(args) > 0 {
				service, err := o.resolveService(command.Context(), client, args[0])
				if err != nil {
					return err
				}
				keys, err := client.ServiceSSHKeys(command.Context(), service.ProjectID, service.ID)
				if err != nil {
					return err
				}
				sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
				rows := [][]string{{"NAME", "FINGERPRINT", "ID", "ATTACHED"}}
				for _, key := range keys {
					rows = append(rows, []string{key.Name, key.Fingerprint, key.KeyID, formatTimestamp(key.AttachedAt.Format(time.RFC3339))})
				}
				return o.print(command, keys, rows)
			}
			keys, err := client.SSHKeys(command.Context())
			if err != nil {
				return err
			}
			sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
			rows := [][]string{{"NAME", "FINGERPRINT", "ID"}}
			for _, key := range keys {
				rows = append(rows, []string{key.Name, key.Fingerprint, key.ID})
			}
			return o.print(command, keys, rows)
		},
	}
}

func (o *options) sshKeyRegisterCommand() *cobra.Command {
	var name string
	command := &cobra.Command{Use: "register [file]", Short: "Register a permanent public key (requires a read-write token)", Args: validateArguments,
		Long: "Register an existing SSH public key. Defaults to ~/.ssh/id_ed25519.pub and your hostname.\nThe key has no expiration. Use ssh-key attach to enable access to a service.",
		RunE: func(command *cobra.Command, args []string) error {
			path := ""
			if len(args) > 0 {
				path = args[0]
			} else {
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				path = filepath.Join(home, ".ssh", "id_ed25519.pub")
			}
			if !command.Flags().Changed("name") {
				var err error
				name, err = os.Hostname()
				if err != nil {
					return fmt.Errorf("read hostname; supply --name: %w", err)
				}
			}
			if strings.TrimSpace(name) == "" {
				return errors.New("--name must not be empty")
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read SSH public key: %w", err)
			}
			publicKey := strings.TrimSpace(string(content))
			if publicKey == "" || strings.Contains(publicKey, "PRIVATE KEY") {
				return errors.New("supply an SSH public key file, usually ending in .pub")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			key, err := client.CreateSSHKey(command.Context(), name, publicKey, time.Time{})
			if err != nil {
				return err
			}
			return o.print(command, key, [][]string{{"Name", key.Name}, {"ID", key.ID}, {"Fingerprint", key.Fingerprint}})
		},
	}
	command.Flags().StringVar(&name, "name", "", "Key name (default: hostname)")
	return command
}

func (o *options) sshKeyDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "delete <name-or-id>", Short: "Delete an unattached SSH key (requires a read-write token)", Args: validateArguments,
		Long: "Delete a registered SSH key. Detach it from all services first.",
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			key, err := resolveSSHKey(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if err := o.confirmResourceDeletion(command, "SSH key", key.Name, key.ID); err != nil {
					return err
				}
			}
			if err := client.DeleteSSHKey(command.Context(), key.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": key.ID, "name": key.Name, "deleted": true}, [][]string{{"Deleted", key.Name}, {"ID", key.ID}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm SSH key deletion")
	return command
}

func (o *options) sshKeyAccessCommand(action string) *cobra.Command {
	return &cobra.Command{Use: action + " <key> <service>", Short: strings.ToUpper(action[:1]) + action[1:] + " an SSH key for a service (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			key, err := resolveSSHKey(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[1])
			if err != nil {
				return err
			}
			if action == "attach" {
				connection, err := client.AttachSSHKey(command.Context(), service.ProjectID, service.ID, key.ID)
				if err != nil {
					return err
				}
				return o.print(command, connection, [][]string{{"Key", key.Name}, {"Service", service.Name}, {"Host", connection.Host}, {"Port", strconv.Itoa(connection.Port)}, {"Username", connection.Username}})
			}
			if err := client.DetachSSHKey(command.Context(), service.ProjectID, service.ID, key.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"keyId": key.ID, "serviceId": service.ID, "detached": true}, [][]string{{"Detached", key.Name}, {"Service", service.Name}})
		},
	}
}

func resolveSSHKey(ctx context.Context, client *api.Client, query string) (api.SSHKey, error) {
	keys, err := client.SSHKeys(ctx)
	if err != nil {
		return api.SSHKey{}, err
	}
	choices := make([]choice, len(keys))
	for i, key := range keys {
		choices[i] = choice{key.ID, key.Name}
	}
	index, err := resolve("SSH key", query, choices)
	if err != nil {
		return api.SSHKey{}, err
	}
	return keys[index], nil
}
