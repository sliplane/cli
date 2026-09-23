// Resource commands focus on discovery and a small set of explicit user actions.
package cli

import (
	"errors"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// projectCommand lists, resolves, and creates projects without mirroring every API route.
func (o *options) projectCommand() *cobra.Command {
	command := &cobra.Command{Use: "project", Short: "List, inspect, and create projects"}
	command.AddCommand(&cobra.Command{
		Use: "list", Short: "List all projects", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			projects, err := client.Projects(command.Context())
			if err != nil {
				return err
			}
			sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
			rows := [][]string{{"NAME"}}
			for _, project := range projects {
				rows = append(rows, []string{project.Name})
			}
			return o.print(command, projects, rows)
		},
	}, &cobra.Command{
		Use: "show <name-or-id>", Short: "Show a project", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			project, err := resolveProject(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			return o.print(command, project, [][]string{{"Name", project.Name}, {"ID", project.ID}})
		},
	}, &cobra.Command{
		Use: "create <name>", Short: "Create a project (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return errors.New("project name must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			project, err := client.CreateProject(command.Context(), args[0])
			if err != nil {
				return err
			}
			return o.print(command, project, [][]string{{"Name", project.Name}, {"ID", project.ID}})
		},
	})
	return command
}

// serverCommand provides server discovery without provisioning infrastructure.
func (o *options) serverCommand() *cobra.Command {
	command := &cobra.Command{Use: "server", Short: "List and inspect servers"}
	command.AddCommand(&cobra.Command{
		Use: "list", Short: "List all servers", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			servers, err := client.Servers(command.Context())
			if err != nil {
				return err
			}
			sort.Slice(servers, func(i, j int) bool { return servers[i].Name < servers[j].Name })
			rows := [][]string{{"NAME", "STATUS", "TYPE", "LOCATION", "IP"}}
			for _, server := range servers {
				rows = append(rows, []string{server.Name, server.Status, server.InstanceType, server.Location, server.IPv4})
			}
			return o.print(command, servers, rows)
		},
	}, &cobra.Command{
		Use: "show <name-or-id>", Short: "Show a server", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			servers, err := client.Servers(command.Context())
			if err != nil {
				return err
			}
			choices := make([]choice, len(servers))
			for index, server := range servers {
				choices[index] = choice{server.ID, server.Name}
			}
			index, err := resolve("server", args[0], choices)
			if err != nil {
				return err
			}
			server, err := client.Server(command.Context(), servers[index].ID)
			if err != nil {
				return err
			}
			return o.print(command, server, [][]string{{"Name", server.Name}, {"ID", server.ID}, {"Status", server.Status}, {"Type", server.InstanceType}, {"Location", server.Location}, {"IPv4", server.IPv4}, {"IPv6", server.IPv6}})
		},
	})
	return command
}

// serviceCommand shares log and deploy implementations with their top-level shortcuts.
func (o *options) serviceCommand() *cobra.Command {
	command := &cobra.Command{Use: "service", Short: "List, inspect, deploy, and read service logs"}
	command.AddCommand(&cobra.Command{
		Use: "list", Short: "List services across projects (or choose --project)", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			services, err := o.services(command.Context(), client)
			if err != nil {
				return err
			}
			if o.output == "json" {
				return o.print(command, services, nil)
			}
			serverNames := map[string]string{}
			if len(services) > 0 {
				servers, err := client.Servers(command.Context())
				if err != nil {
					return err
				}
				for _, server := range servers {
					serverNames[server.ID] = server.Name
				}
			}
			rows := [][]string{{"NAME", "PROJECT", "STATUS", "SERVER"}}
			for _, service := range services {
				serverName := serverNames[service.ServerID]
				if serverName == "" {
					serverName = service.ServerID
				}
				rows = append(rows, []string{service.Name, service.ProjectName, service.Status, serverName})
			}
			return o.print(command, services, rows)
		},
	}, &cobra.Command{
		Use: "show <name-or-id>", Short: "Show a service", Args: validateArguments,
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
			return o.print(command, service, [][]string{{"Name", service.Name}, {"ID", service.ID}, {"Status", service.Status}, {"Project", service.ProjectID}, {"Server", service.ServerID}, {"Created", service.CreatedAt}})
		},
	}, o.logsCommand(), o.deployCommand())
	return command
}

// deployCommand reports request acceptance rather than claiming a completed deployment.
func (o *options) deployCommand() *cobra.Command {
	var tag string
	command := &cobra.Command{
		Use: "deploy <name-or-id>", Short: "Deploy a service (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			service, err := o.resolveService(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			if err := client.Deploy(command.Context(), service.ProjectID, service.ID, tag); err != nil {
				return err
			}
			return o.print(command, map[string]any{"accepted": true, "serviceId": service.ID, "projectId": service.ProjectID}, [][]string{{"Deployment accepted", service.Name}})
		},
	}
	command.Flags().StringVar(&tag, "tag", "", "Image tag to deploy (image-based services only)")
	return command
}
