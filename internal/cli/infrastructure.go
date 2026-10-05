package cli

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) serverCreateCommand() *cobra.Command {
	input := api.CreateServerInput{}
	command := &cobra.Command{Use: "create <name>", Short: "Create a server (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			input.Name = args[0]
			if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.InstanceType) == "" || strings.TrimSpace(input.Location) == "" {
				return errors.New("server name, --instance-type, and --location must not be empty")
			}
			if err := validateBillingCycle(input.BillingCycle); err != nil {
				return err
			}
			if command.Flags().Changed("disk-size") {
				if err := validateServerDiskSize(input.DiskSizeGB); err != nil {
					return err
				}
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := client.CreateServer(command.Context(), input)
			if err != nil {
				return err
			}
			return o.printServer(command, server)
		},
	}
	command.Flags().StringVar(&input.InstanceType, "instance-type", "base", "Instance type")
	command.Flags().StringVar(&input.Location, "location", "ger", "Region: ger, fin, us-east, us-west, sin, conf")
	command.Flags().StringVar(&input.BillingCycle, "billing-cycle", "hourly", "Billing cycle: hourly, monthly, yearly")
	command.Flags().IntVar(&input.DiskSizeGB, "disk-size", 0, "Larger data disk in GB (default: bundled disk)")
	return command
}

func (o *options) serverScaleCommand() *cobra.Command {
	var billingCycle string
	command := &cobra.Command{Use: "scale <name-or-id> <instance-type>", Short: "Scale up a server (requires a read-write token)", Args: validateArguments,
		Long: "Scale up to an explicit instance type. Services will be unavailable for a few minutes.\nBilling defaults to hourly; use --billing-cycle to select monthly or yearly.\nReturns when the request is accepted; use server show to check progress.",
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("instance type must not be empty")
			}
			if err := validateBillingCycle(billingCycle); err != nil {
				return err
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := resolveServer(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if err := client.ScaleServer(command.Context(), server.ID, args[1], billingCycle); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": server.ID, "name": server.Name, "instanceType": args[1], "accepted": true}, [][]string{{"Server", server.Name}, {"Type", args[1]}, {"Request", "scale accepted"}})
		},
	}
	command.Flags().StringVar(&billingCycle, "billing-cycle", "hourly", "Billing cycle: hourly, monthly, yearly")
	return command
}

func (o *options) serverDiskCommand() *cobra.Command {
	return &cobra.Command{Use: "resize-disk <name-or-id> <size-gb>", Short: "Grow a server's data disk (requires a read-write token)", Args: validateArguments,
		Long: "Grow the data disk to 50, 100, 250, 500, or 1000 GB. Disks cannot shrink.\nReturns when the request is accepted; use server show to check progress.",
		RunE: func(command *cobra.Command, args []string) error {
			size, err := strconv.Atoi(args[1])
			if err != nil {
				return errors.New("disk size must be an integer in GB")
			}
			if err := validateServerDiskSize(size); err != nil {
				return err
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := resolveServer(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if size <= server.DiskSizeGB {
				return fmt.Errorf("disk size must be larger than the current %d GB", server.DiskSizeGB)
			}
			if err := client.ScaleServerDisk(command.Context(), server.ID, size); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": server.ID, "name": server.Name, "diskSizeGb": size, "accepted": true}, [][]string{{"Server", server.Name}, {"Disk", args[1] + " GB"}, {"Request", "disk resize accepted"}})
		},
	}
}

func validateBillingCycle(value string) error {
	if !slices.Contains([]string{"hourly", "monthly", "yearly"}, value) {
		return errors.New("--billing-cycle must be hourly, monthly, or yearly")
	}
	return nil
}

func validateServerDiskSize(size int) error {
	if !slices.Contains([]int{50, 100, 250, 500, 1000}, size) {
		return errors.New("disk size must be 50, 100, 250, 500, or 1000 GB")
	}
	return nil
}

func resolveServer(ctx context.Context, client *api.Client, query string) (api.Server, error) {
	servers, err := client.Servers(ctx)
	if err != nil {
		return api.Server{}, err
	}
	choices := make([]choice, len(servers))
	for i, server := range servers {
		choices[i] = choice{server.ID, server.Name}
	}
	index, err := resolve("server", query, choices)
	if err != nil {
		return api.Server{}, err
	}
	return servers[index], nil
}

func (o *options) printServer(command *cobra.Command, server api.Server) error {
	rows := [][]string{{"Name", server.Name}, {"ID", server.ID}, {"Status", server.Status}, {"Type", server.InstanceType}, {"Location", server.Location}}
	if server.VCPU > 0 {
		rows = append(rows, []string{"CPU", strconv.Itoa(server.VCPU) + " vCPU"})
	}
	if server.MemoryMB > 0 {
		rows = append(rows, []string{"Memory", strconv.Itoa(server.MemoryMB) + " MB"})
	}
	if server.DiskSizeGB > 0 {
		rows = append(rows, []string{"Disk", strconv.Itoa(server.DiskSizeGB) + " GB"})
	}
	if server.IPv4 != "" {
		rows = append(rows, []string{"IPv4", server.IPv4})
	}
	if server.IPv6 != "" {
		rows = append(rows, []string{"IPv6", server.IPv6})
	}
	return o.print(command, server, rows)
}

func (o *options) serverDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "delete <name-or-id>", Short: "Delete a server (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := resolveServer(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if err := o.confirmResourceDeletion(command, "server", server.Name, server.ID); err != nil {
					return err
				}
			}
			if err := client.DeleteServer(command.Context(), server.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": server.ID, "name": server.Name, "deleted": true}, [][]string{{"Deleted", server.Name}, {"ID", server.ID}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm permanent server deletion")
	return command
}

func (o *options) projectRenameCommand() *cobra.Command {
	return &cobra.Command{Use: "rename <name-or-id> <new-name>", Short: "Rename a project (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("project name must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			project, err := resolveProject(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			project, err = client.RenameProject(command.Context(), project.ID, args[1])
			if err != nil {
				return err
			}
			return o.print(command, project, [][]string{{"Name", project.Name}, {"ID", project.ID}})
		},
	}
}

func (o *options) projectDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{Use: "delete <name-or-id>", Short: "Delete an empty project (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			project, err := resolveProject(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if err := o.confirmResourceDeletion(command, "project", project.Name, project.ID); err != nil {
					return err
				}
			}
			if err := client.DeleteProject(command.Context(), project.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": project.ID, "name": project.Name, "deleted": true}, [][]string{{"Deleted", project.Name}, {"ID", project.ID}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm permanent project deletion")
	return command
}

// confirmResourceDeletion follows the existing terminal confirmation and --yes convention.
func (o *options) confirmResourceDeletion(command *cobra.Command, kind, name, id string) error {
	if !o.canPrompt(command) {
		return fmt.Errorf("%s deletion requires confirmation; use --yes or rerun in a terminal", kind)
	}
	approved, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), fmt.Sprintf("Permanently delete %s %q (%s)?", kind, name, terminalText(id)))
	if err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("%s deletion canceled", kind)
	}
	return nil
}
