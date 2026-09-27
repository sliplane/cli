// Service creation exposes both public API deployment sources while validating incompatible settings before writes.
package cli

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

type serviceCreateFlags struct {
	image, repo, server, protocol string
	cmd, healthcheck, dockerfile  string
	public, autoDeploy            bool
	deployment                    api.ServiceDeployment
	env, publicEnv, envFiles      []string
	volumes, mounts               []string
}

// serviceCreateCommand reuses preset destination selection and credential-free creation receipts.
func (o *options) serviceCreateCommand() *cobra.Command {
	var flags serviceCreateFlags
	command := &cobra.Command{
		Use: "create [name]", Short: "Create a service from an image or GitHub repository (requires a read-write token)", Args: validateArguments,
		Long:    "Create a new service. In a terminal, missing name, source, and destination choices are prompted.\nSupply --image or --repo to skip source selection; scripts must provide a name and one source.\nServices are private by default;\nuse --public to expose one, with --protocol http (default), tcp, or udp.\nChoose an existing project and server by name or ID, or interactively when there are multiple choices.\nRepository defaults come from the API: main branch, automatic deployment, Dockerfile, and context '.'.\nUse --dockerfile='' for automatic builds without a Dockerfile. GitHub access must already be connected.\nEnvironment values are literal: no quote removal, interpolation, or shell execution. Environment files\ncontain KEY=value or KEY lines, with blank lines and # comments allowed. Files are applied in order,\nthen --env, then --public-env; the last value for a key wins. --env and file values are stored as secrets.\nNew volumes use --volume NAME:/path; existing volumes use --mount ID:/path.\nEvery invocation creates a new service. Check service show and logs for deployment readiness.",
		Example: "  sliplane service create web\n  sliplane service create web --image docker.io/library/nginx:1.28 --public --project production --server main\n  sliplane service create api --repo https://github.com/acme/api --branch main --public --env DATABASE_URL\n  sliplane service create worker --image ghcr.io/acme/worker:v1 --registry-auth credential_123 --volume data:/data",
		RunE: func(command *cobra.Command, args []string) error {
			for _, flag := range []string{"server", "project"} {
				value, _ := command.Flags().GetString(flag)
				if command.Flags().Changed(flag) && strings.TrimSpace(value) == "" {
					return fmt.Errorf("--%s must not be empty", flag)
				}
			}
			name, err := o.completeServiceCreate(command, args)
			if err != nil {
				return err
			}
			input, err := flags.serviceInput(command, name)
			if err != nil {
				return err
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			project, server, err := o.deploymentDestination(command, client, flags.server)
			if err != nil {
				return err
			}
			input.ServerID = server.id
			service, err := client.CreateService(command.Context(), project.id, input)
			if err != nil {
				return err
			}
			return o.printCreatedService(command, service, "", project, server)
		},
	}
	command.Flags().StringVar(&flags.image, "image", "", "Container image reference, without http:// or https://")
	command.Flags().StringVar(&flags.repo, "repo", "", "GitHub repository URL (https://github.com/owner/repo)")
	command.Flags().StringVar(&flags.server, "server", "", "Existing server name or ID")
	command.Flags().StringVar(&flags.deployment.RegistryAuthenticationID, "registry-auth", "", "Existing registry credential ID (images only)")
	command.Flags().StringVar(&flags.deployment.Branch, "branch", "", "Repository branch (API default: main)")
	command.Flags().BoolVar(&flags.autoDeploy, "auto-deploy", true, "Automatically deploy repository pushes; use --auto-deploy=false to disable")
	command.Flags().StringVar(&flags.dockerfile, "dockerfile", "", "Repository Dockerfile path (API default: Dockerfile; empty selects automatic builds)")
	command.Flags().StringVar(&flags.deployment.DockerContext, "docker-context", "", "Repository Docker build context (API default: .)")
	command.Flags().StringArrayVar(&flags.deployment.IncludePaths, "include-path", nil, "Repository path glob that triggers deployment (repeatable)")
	command.Flags().StringArrayVar(&flags.deployment.IgnorePaths, "ignore-path", nil, "Repository path glob ignored for deployment (repeatable)")
	command.Flags().BoolVar(&flags.public, "public", false, "Expose the service publicly")
	command.Flags().StringVar(&flags.protocol, "protocol", "http", "Public network protocol: http, tcp, or udp (requires --public)")
	command.Flags().StringVar(&flags.healthcheck, "healthcheck", "", "HTTP health check path (requires public HTTP; API default: /)")
	command.Flags().StringVar(&flags.cmd, "cmd", "", "Override the container command")
	command.Flags().StringArrayVar(&flags.env, "env", nil, "Secret environment variable: KEY=value, or KEY from the local environment (repeatable)")
	command.Flags().StringArrayVar(&flags.publicEnv, "public-env", nil, "Non-secret environment variable: KEY=value, or KEY from the local environment (repeatable)")
	command.Flags().StringArrayVar(&flags.envFiles, "env-file", nil, "Read literal environment entries from a file, stored as secrets (repeatable)")
	command.Flags().StringArrayVar(&flags.volumes, "volume", nil, "Create and mount a new volume: NAME:/absolute/path (repeatable)")
	command.Flags().StringArrayVar(&flags.mounts, "mount", nil, "Mount an existing volume: ID:/absolute/path (repeatable)")
	return command
}

// serviceInput assembles a validated request before resolving credentials or contacting Sliplane.
func (flags serviceCreateFlags) serviceInput(command *cobra.Command, name string) (api.CreateServiceInput, error) {
	input := api.CreateServiceInput{Name: name, Cmd: flags.cmd}
	if strings.TrimSpace(name) == "" {
		return input, errors.New("service name must not be empty")
	}
	deployment, err := flags.serviceDeployment(command)
	if err != nil {
		return input, err
	}
	input.Deployment = deployment
	input.Network.Public = flags.public
	if flags.protocol != "http" && flags.protocol != "tcp" && flags.protocol != "udp" {
		return input, errors.New("--protocol must be http, tcp, or udp")
	}
	if command.Flags().Changed("protocol") && !flags.public {
		return input, errors.New("--protocol requires --public")
	}
	if flags.public {
		input.Network.Protocol = flags.protocol
	}
	if command.Flags().Changed("healthcheck") {
		if !flags.public || flags.protocol != "http" {
			return input, errors.New("--healthcheck requires --public with --protocol http")
		}
		if !strings.HasPrefix(flags.healthcheck, "/") {
			return input, errors.New("--healthcheck must be a path starting with /")
		}
		input.Healthcheck = flags.healthcheck
	}
	input.Volumes, err = serviceVolumes(flags.volumes, flags.mounts)
	if err != nil {
		return input, err
	}
	input.Env, err = serviceEnvironment(flags.envFiles, flags.env, flags.publicEnv)
	return input, err
}

// serviceDeployment keeps repository-only and image-only fields out of the opposite source's request.
func (flags serviceCreateFlags) serviceDeployment(command *cobra.Command) (api.ServiceDeployment, error) {
	deployment := flags.deployment
	imageSet, repoSet := command.Flags().Changed("image"), command.Flags().Changed("repo")
	if imageSet == repoSet {
		return deployment, errors.New("provide exactly one of --image or --repo")
	}
	for _, name := range []string{"image", "repo", "registry-auth", "branch", "docker-context"} {
		value, _ := command.Flags().GetString(name)
		if command.Flags().Changed(name) && strings.TrimSpace(value) == "" {
			return deployment, fmt.Errorf("--%s must not be empty", name)
		}
	}
	for _, name := range []string{"branch", "auto-deploy", "dockerfile", "docker-context", "include-path", "ignore-path"} {
		if imageSet && command.Flags().Changed(name) {
			return deployment, fmt.Errorf("--%s requires --repo", name)
		}
	}
	if imageSet {
		if err := validateServiceImage(flags.image); err != nil {
			return deployment, err
		}
		deployment.URL = flags.image
		return deployment, nil
	}
	if command.Flags().Changed("registry-auth") {
		return deployment, errors.New("--registry-auth requires --image")
	}
	if err := validateServiceRepository(flags.repo); err != nil {
		return deployment, err
	}
	deployment.URL = flags.repo
	if command.Flags().Changed("auto-deploy") {
		deployment.AutoDeploy = &flags.autoDeploy
	}
	if command.Flags().Changed("dockerfile") {
		deployment.DockerfilePath = &flags.dockerfile
	}
	for _, pattern := range append(append([]string{}, deployment.IncludePaths...), deployment.IgnorePaths...) {
		if strings.TrimSpace(pattern) == "" {
			return deployment, errors.New("--include-path and --ignore-path must not be empty")
		}
	}
	return deployment, nil
}

// validateServiceImage shares image validation between flags and interactive input.
func validateServiceImage(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("--image must not be empty")
	}
	if strings.Contains(value, "://") || strings.ContainsAny(value, " \t\r\n") {
		return errors.New("--image must be a container image reference without a URL scheme or whitespace")
	}
	return nil
}

// validateServiceRepository accepts repository URLs without echoing potentially embedded credentials.
func validateServiceRepository(value string) error {
	repository, err := url.Parse(value)
	if err != nil {
		return errors.New("--repo must be an HTTPS GitHub repository URL")
	}
	parts := strings.Split(strings.Trim(repository.Path, "/"), "/")
	if repository.Scheme != "https" || repository.Host != "github.com" || repository.User != nil || repository.RawQuery != "" || repository.Fragment != "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errors.New("--repo must be https://github.com/owner/repo without credentials, query, or fragment")
	}
	return nil
}

// serviceVolumes separates new names from existing IDs and rejects duplicate container mount paths.
func serviceVolumes(volumes, mounts []string) ([]api.ServiceVolume, error) {
	entries := []api.ServiceVolume{}
	paths := map[string]bool{}
	for _, group := range []struct {
		flag   string
		values []string
	}{{"volume", volumes}, {"mount", mounts}} {
		for _, value := range group.values {
			identifier, mountPath, found := strings.Cut(value, ":")
			if !found || strings.TrimSpace(identifier) == "" || !strings.HasPrefix(mountPath, "/") {
				return nil, fmt.Errorf("--%s requires a nonempty name or ID followed by :/absolute/path", group.flag)
			}
			if paths[mountPath] {
				return nil, fmt.Errorf("duplicate volume mount path %q", mountPath)
			}
			paths[mountPath] = true
			entry := api.ServiceVolume{MountPath: mountPath}
			if group.flag == "volume" {
				entry.Name = identifier
			} else {
				entry.ID = identifier
			}
			entries = append(entries, entry)
		}
	}
	return entries, nil
}
