package cli

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) selectServer(command *cobra.Command, client *api.Client, query string) (choice, error) {
	servers, err := client.Servers(command.Context())
	if err != nil {
		return choice{}, err
	}
	choices := make([]choice, len(servers))
	for index, server := range servers {
		choices[index] = choice{server.ID, server.Name}
	}
	return o.chooseDeploymentResource(command, "server", query, choices)
}

func resolveVolume(query string, volumes []api.Volume) (api.Volume, error) {
	choices := make([]choice, len(volumes))
	for index, volume := range volumes {
		choices[index] = choice{volume.ID, volume.Name}
	}
	index, err := resolve("volume", query, choices)
	if err != nil {
		return api.Volume{}, err
	}
	return volumes[index], nil
}

func (o *options) printVolume(command *cobra.Command, volume api.Volume) error {
	rows := [][]string{{"Name", volume.Name}, {"ID", volume.ID}}
	if volume.MountPath != "" {
		rows = append(rows, []string{"Mount", volume.MountPath})
	}
	return o.print(command, volume, rows)
}

func (o *options) serverVolumeCommand() *cobra.Command {
	var serverName string
	command := &cobra.Command{Use: "volume", Short: "List, create, and rename server volumes"}
	command.PersistentFlags().StringVar(&serverName, "server", "", "Server name or ID (auto-selected when only one exists)")
	command.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List volumes on a server", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := o.selectServer(command, client, serverName)
			if err != nil {
				return err
			}
			volumes, err := client.ServerVolumes(command.Context(), server.id)
			if err != nil {
				return err
			}
			rows := [][]string{{"NAME", "ID"}}
			for _, volume := range volumes {
				rows = append(rows, []string{volume.Name, volume.ID})
			}
			return o.print(command, volumes, rows)
		},
	}, &cobra.Command{
		Use: "create <name>", Short: "Create a volume on a server", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[0]) == "" {
				return errors.New("volume name must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := o.selectServer(command, client, serverName)
			if err != nil {
				return err
			}
			volume, err := client.CreateVolume(command.Context(), server.id, args[0])
			if err != nil {
				return err
			}
			return o.printVolume(command, volume)
		},
	}, &cobra.Command{
		Use: "rename <volume> <name>", Short: "Rename a volume without redeploying services", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if strings.TrimSpace(args[1]) == "" {
				return errors.New("volume name must not be empty")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			server, err := o.selectServer(command, client, serverName)
			if err != nil {
				return err
			}
			volumes, err := client.ServerVolumes(command.Context(), server.id)
			if err != nil {
				return err
			}
			volume, err := resolveVolume(args[0], volumes)
			if err != nil {
				return err
			}
			volume, err = client.RenameVolume(command.Context(), server.id, volume.ID, args[1])
			if err != nil {
				return err
			}
			return o.printVolume(command, volume)
		},
	})
	return command
}

func (o *options) serviceVolumeCommand() *cobra.Command {
	command := &cobra.Command{Use: "volume", Short: "Manage service volume mounts"}
	command.AddCommand(&cobra.Command{
		Use: "list <service>", Aliases: []string{"ls"}, Short: "List a service's mounted volumes", Args: validateArguments,
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
			volumes := []api.Volume{}
			if len(service.Volumes) > 0 {
				if err := json.Unmarshal(service.Volumes, &volumes); err != nil {
					return err
				}
			}
			if volumes == nil {
				volumes = []api.Volume{}
			}
			rows := [][]string{{"NAME", "MOUNT", "ID"}}
			for _, volume := range volumes {
				rows = append(rows, []string{volume.Name, volume.MountPath, volume.ID})
			}
			return o.print(command, volumes, rows)
		},
	})
	for _, action := range []struct{ use, short string }{
		{"attach <service> <volume> <mount-path>", "Attach a volume at the given path and redeploy"},
		{"detach <service> <volume>", "Detach a volume and redeploy; keep its data on the server"},
		{"update <service> <volume> <mount-path>", "Change a volume's mount path and redeploy"},
	} {
		command.AddCommand(&cobra.Command{Use: action.use, Short: action.short, Args: validateArguments, RunE: o.changeServiceVolume})
	}
	return command
}

func (o *options) changeServiceVolume(command *cobra.Command, args []string) error {
	mountPath := ""
	if command.Name() != "detach" {
		mountPath = args[2]
		if !strings.HasPrefix(mountPath, "/") {
			return errors.New("mount path must be an absolute container path")
		}
	}
	client, _, err := o.client(command)
	if err != nil {
		return err
	}
	service, err := o.resolveService(command.Context(), client, args[0])
	if err != nil {
		return err
	}
	var volumes []api.Volume
	if command.Name() == "attach" {
		volumes, err = client.ServerVolumes(command.Context(), service.ServerID)
	} else {
		service, err = client.Service(command.Context(), service.ProjectID, service.ID)
		if err == nil && len(service.Volumes) > 0 {
			err = json.Unmarshal(service.Volumes, &volumes)
		}
	}
	if err != nil {
		return err
	}
	volume, err := resolveVolume(args[1], volumes)
	if err != nil {
		return err
	}
	switch command.Name() {
	case "attach":
		volume, err = client.AttachVolume(command.Context(), service.ProjectID, service.ID, api.ServiceVolume{ID: volume.ID, MountPath: mountPath})
	case "update":
		volume, err = client.UpdateVolumeMount(command.Context(), service.ProjectID, service.ID, volume.ID, mountPath)
	case "detach":
		if err := client.DetachVolume(command.Context(), service.ProjectID, service.ID, volume.ID); err != nil {
			return err
		}
		return o.print(command, map[string]any{"id": volume.ID, "name": volume.Name, "detached": true}, [][]string{{"Detached", volume.Name}, {"ID", volume.ID}})
	}
	if err != nil {
		return err
	}
	return o.printVolume(command, volume)
}
