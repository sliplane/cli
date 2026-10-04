package cli

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

func (o *options) projectCommand() *cobra.Command {
	var all bool
	command := &cobra.Command{Use: "project", Short: "List, inspect, and create projects"}
	list := &cobra.Command{
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
			if all {
				rows[0] = append(rows[0], "ID")
			}
			for _, project := range projects {
				row := []string{project.Name}
				if all {
					row = append(row, project.ID)
				}
				rows = append(rows, row)
			}
			return o.print(command, projects, rows)
		},
	}
	list.Flags().BoolVarP(&all, "all", "a", false, "Show additional columns (does not change which resources are listed)")
	command.AddCommand(list, &cobra.Command{
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

func (o *options) serverCommand() *cobra.Command {
	var all bool
	command := &cobra.Command{Use: "server", Short: "List and inspect servers"}
	list := &cobra.Command{
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
			if all {
				rows[0] = append(rows[0], "ID", "VCPU", "MEMORY", "DISK", "IPV6", "CREATED")
			}
			for _, server := range servers {
				row := []string{server.Name, server.Status, server.InstanceType, server.Location, server.IPv4}
				if all {
					row = append(row, server.ID, strconv.Itoa(server.VCPU), strconv.Itoa(server.MemoryMB)+" MB", strconv.Itoa(server.DiskSizeGB)+" GB", server.IPv6, formatTimestamp(server.CreatedAt))
				}
				rows = append(rows, row)
			}
			return o.print(command, servers, rows)
		},
	}
	list.Flags().BoolVarP(&all, "all", "a", false, "Show additional columns (does not change which resources are listed)")
	command.AddCommand(list, &cobra.Command{
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

func (o *options) serviceCommand() *cobra.Command {
	var all bool
	command := &cobra.Command{Use: "service", Short: "Inspect, deploy, and manage services"}
	list := &cobra.Command{
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
			if all {
				rows[0] = append(rows[0], "ID", "PROJECT ID", "SERVER ID", "CREATED")
			}
			for _, service := range services {
				serverName := serverNames[service.ServerID]
				if serverName == "" {
					serverName = service.ServerID
				}
				row := []string{service.Name, service.ProjectName, service.Status, serverName}
				if all {
					row = append(row, service.ID, service.ProjectID, service.ServerID, formatTimestamp(service.CreatedAt))
				}
				rows = append(rows, row)
			}
			return o.print(command, services, rows)
		},
	}
	list.Flags().BoolVarP(&all, "all", "a", false, "Show additional columns (does not change which resources are listed)")
	command.AddCommand(list, &cobra.Command{
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
			return o.printService(command, service)
		},
	}, o.logsCommand(), o.deployCommand(), o.serviceCreateCommand(), o.serviceUpdateCommand(), o.serviceRedeployCommand(), o.serviceDeleteCommand(),
		o.serviceLifecycleCommand("pause", "Pause a service"), o.serviceLifecycleCommand("unpause", "Unpause a service"))
	return command
}
