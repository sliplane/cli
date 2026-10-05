package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) serviceUpdateCommand() *cobra.Command {
	var name, healthcheck, cmd string
	command := &cobra.Command{
		Use: "update <name-or-id>", Short: "Update service settings (requires a read-write token)", Args: validateArguments,
		Long:    "Update service settings or deployment configuration. Only supplied fields are changed.\nUse --cmd='' to remove the command override. Use service redeploy to deploy an existing service,\noptionally with a new image tag. Deployment type cannot be changed. Check service show and logs before reporting readiness.",
		Example: "  sliplane service update api --project production --healthcheck /health\n  sliplane service update api --name backend --cmd='npm start'",
		RunE: func(command *cobra.Command, args []string) error {
			input := api.UpdateServiceInput{}
			if command.Flags().Changed("name") {
				if strings.TrimSpace(name) == "" {
					return errors.New("--name must not be empty")
				}
				input.Name = &name
			}
			if command.Flags().Changed("healthcheck") {
				if !strings.HasPrefix(healthcheck, "/") {
					return errors.New("--healthcheck must be a path starting with /")
				}
				input.Healthcheck = &healthcheck
			}
			if command.Flags().Changed("cmd") {
				input.Cmd = &cmd
			}
			deploymentChanged := false
			for _, flag := range deploymentFlags {
				if command.Flags().Changed(flag) {
					deploymentChanged = true
				}
			}
			if input.Name == nil && input.Healthcheck == nil && input.Cmd == nil && !deploymentChanged {
				return errors.New("provide --name, --healthcheck, --cmd, or a deployment flag")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if deploymentChanged {
				service, err = client.Service(command.Context(), service.ProjectID, service.ID)
				if err != nil {
					return err
				}
				input.Deployment, err = updatedDeployment(command, service.Deployment)
				if err != nil {
					return err
				}
			}
			service, err = client.UpdateService(command.Context(), service.ProjectID, service.ID, input)
			if err != nil {
				return err
			}
			return o.printService(command, service)
		},
	}
	command.Flags().StringVar(&name, "name", "", "New service name")
	command.Flags().StringVar(&healthcheck, "healthcheck", "", "HTTP health check path (e.g. /health)")
	command.Flags().StringVar(&cmd, "cmd", "", "Command override (empty removes the override)")
	addDeploymentUpdateFlags(command)
	return command
}

func (o *options) serviceRedeployCommand() *cobra.Command {
	var tag string
	command := &cobra.Command{
		Use: "redeploy <name-or-id>", Short: "Redeploy an existing service (requires a read-write token)", Args: validateArguments,
		Long:    "Trigger a new deployment of an existing service. Optionally select a new tag for an image-based service.\nThis returns when the request is accepted; use service show and logs to check deployment progress.",
		Example: "  sliplane service redeploy api --project production\n  sliplane service redeploy api --tag v2.0.0",
		RunE: func(command *cobra.Command, args []string) error {
			if command.Flags().Changed("tag") && strings.TrimSpace(tag) == "" {
				return errors.New("--tag must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if err := client.RedeployService(command.Context(), service.ProjectID, service.ID, tag); err != nil {
				return err
			}
			return o.printServiceAction(command, service, "redeploy")
		},
	}
	command.Flags().StringVar(&tag, "tag", "", "New image tag (image-based services only)")
	return command
}

func (o *options) serviceLifecycleCommand(action, description string) *cobra.Command {
	return &cobra.Command{
		Use: action + " <name-or-id>", Short: description + " (requires a read-write token)", Args: validateArguments,
		Long:    description + ". This returns when the request is accepted.\nUse service show to check the current status.",
		Example: "  sliplane service " + action + " api --project production",
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if err := client.ServiceAction(command.Context(), service.ProjectID, service.ID, action); err != nil {
				return err
			}
			return o.printServiceAction(command, service, action)
		},
	}
}

func (o *options) serviceDeleteCommand() *cobra.Command {
	var yes bool
	command := &cobra.Command{
		Use: "delete <name-or-id>", Short: "Delete a service (requires a read-write token)", Args: validateArguments,
		Long:    "Delete an existing service. Requires confirmation in a terminal, or --yes for noninteractive use.",
		Example: "  sliplane service delete api --project staging\n  sliplane service delete service_example --yes",
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if !yes {
				if !o.canPrompt(command) {
					return errors.New("service deletion requires confirmation; use --yes or rerun in a terminal")
				}
				question := fmt.Sprintf("Permanently delete service %q (%s) in project %q?", service.Name, terminalText(service.ID), service.ProjectID)
				approved, err := confirm(command.Context(), command.InOrStdin(), command.ErrOrStderr(), question)
				if err != nil {
					return err
				}
				if !approved {
					return errors.New("service deletion canceled")
				}
			}
			if err := client.DeleteService(command.Context(), service.ProjectID, service.ID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"id": service.ID, "name": service.Name, "projectId": service.ProjectID, "deleted": true}, [][]string{{"Deleted", service.Name}, {"ID", service.ID}, {"Project", service.ProjectID}})
		},
	}
	command.Flags().BoolVarP(&yes, "yes", "y", false, "Confirm permanent service deletion")
	return command
}

func (o *options) printServiceAction(command *cobra.Command, service api.Service, action string) error {
	return o.print(command, map[string]any{"id": service.ID, "name": service.Name, "projectId": service.ProjectID, "action": action, "accepted": true}, [][]string{{"Name", service.Name}, {"ID", service.ID}, {"Project", service.ProjectID}, {"Request", action + " accepted"}})
}

func (o *options) printService(command *cobra.Command, service api.Service) error {
	rows := [][]string{{"Name", service.Name}, {"ID", service.ID}, {"Status", service.Status}, {"Project", service.ProjectID}, {"Server", service.ServerID}, {"Created", formatTimestamp(service.CreatedAt)}}
	details, err := serviceDetailRows(service)
	if err != nil {
		return err
	}
	return o.print(command, service, append(rows, details...))
}

// serviceDetailRows uses the same source and access fields for creation and inspection.
func serviceDetailRows(service api.Service) ([][]string, error) {
	var rows [][]string
	if len(service.Deployment) > 0 {
		var deployment api.ServiceDeployment
		if err := json.Unmarshal(service.Deployment, &deployment); err != nil {
			return nil, err
		}
		if strings.HasPrefix(deployment.URL, "https://github.com/") {
			rows = append(rows, []string{"Repository", deployment.URL})
			if deployment.Branch != "" {
				rows = append(rows, []string{"Branch", deployment.Branch})
			}
		} else if deployment.URL != "" {
			rows = append(rows, []string{"Image", deployment.URL})
		}
	}
	if len(service.Network) > 0 {
		var network api.ServiceNetworkDetails
		if err := json.Unmarshal(service.Network, &network); err != nil {
			return nil, err
		}
		access := "private"
		if network.Public {
			access = strings.TrimSpace("public " + strings.ToUpper(network.Protocol))
		}
		rows = append(rows, []string{"Access", access})
		if network.Public && network.ManagedDomain != "" {
			if network.Protocol == "http" {
				rows = append(rows, []string{"URL", "https://" + network.ManagedDomain})
			} else {
				rows = append(rows, []string{"Hostname", network.ManagedDomain})
			}
		}
		if network.InternalDomain != "" {
			rows = append(rows, []string{"Internal", network.InternalDomain})
		}
		for _, domain := range network.CustomDomains {
			rows = append(rows, []string{"Custom domain", domain.Domain + " (" + domain.Status + ")"})
		}
	}
	return rows, nil
}
