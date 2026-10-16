package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
	"sliplane-cli/internal/presets"
)

type options struct {
	s3Region    string
	profileName string
	preferences config.Config
	identity    *api.Identity
	token       string
	output      string
	project     string
	configPath  string
	baseURL     string
	catalogURL  string
}

func NewCommand() *cobra.Command { return newCommand(api.BaseURL, presets.URL) }

func newCommand(baseURL, catalogURL string) *cobra.Command {
	options := &options{baseURL: baseURL, catalogURL: catalogURL}
	root := &cobra.Command{
		Use:               "sliplane",
		Short:             "Manage Sliplane from your terminal",
		Long:              "Manage Sliplane projects, services, servers, S3 buckets, and PostgreSQL databases by name or ID.",
		SilenceUsage:      true,
		SilenceErrors:     true,
		PersistentPreRunE: func(command *cobra.Command, _ []string) error { return options.load(command) },
	}
	root.PersistentFlags().StringVar(&options.profileName, "profile", "", "Profile name (overrides SLIPLANE_PROFILE and active profile)")
	root.PersistentFlags().StringVar(&options.token, "token", "", "API token (overrides SLIPLANE_TOKEN and OS keychain)")
	root.PersistentFlags().StringVarP(&options.output, "output", "o", "text", "Output format: text or json (JSON Lines when following logs)")
	root.PersistentFlags().StringVarP(&options.project, "project", "p", "", "Project name or ID (overrides config default)")
	root.PersistentFlags().StringVar(&options.configPath, "config", "", "Config file (default: OS user config directory/sliplane/config.json)")
	root.AddCommand(options.versionCommand(), options.agentCommand(), options.authCommand(), options.profileCommand(), options.configCommand(), options.projectCommand(), options.serverCommand(), options.serviceCommand(), options.s3Command(), options.postgresCommand(), options.logsCommand(), options.deployCommand(), options.presetCommand(), options.sshCommand(), options.registryCommand(), options.sshKeyCommand())
	root.AddCommand(options.tunnelCommand())
	return root
}

func (o *options) load(command *cobra.Command) error {
	if o.configPath == "" {
		path, err := config.DefaultPath()
		if err != nil {
			return err
		}
		o.configPath = path
	}
	preferences, err := config.Load(o.configPath)
	if err != nil {
		return err
	}
	o.preferences = preferences
	if !command.Flags().Changed("profile") {
		o.profileName = os.Getenv("SLIPLANE_PROFILE")
		if o.profileName == "" {
			o.profileName = preferences.ActiveProfile
		}
	} else if o.profileName == "" {
		return errors.New("--profile must not be empty")
	}
	if o.profileName != "" {
		if err := config.ValidateProfileName(o.profileName); err != nil {
			return err
		}
		_, exists := preferences.Profiles[o.profileName]
		isLogin := command.Parent() != nil && command.Parent().Name() == "auth" && command.Name() == "login"
		isProfileCommand := command.Parent() != nil && command.Parent().Name() == "profile"
		if !exists && !isLogin && !isProfileCommand {
			return fmt.Errorf("profile %q not found; run sliplane auth login --profile %s", o.profileName, o.profileName)
		}
	}
	if !command.Flags().Changed("output") && preferences.Output != "" {
		o.output = preferences.Output
	}
	if !command.Flags().Changed("project") {
		o.project = preferences.Profiles[o.profileName].Project
	}
	if o.output != "text" && o.output != "json" {
		return errors.New("--output must be text or json")
	}
	return nil
}

func (o *options) client(command *cobra.Command) (*api.Client, string, error) {
	token, source, err := auth.Resolve(o.token, command.Flags().Changed("token"), os.Getenv("SLIPLANE_TOKEN"), o.preferences.Profiles[o.profileName].CredentialID)
	if err != nil {
		return nil, "", err
	}
	client := api.New(o.baseURL, token)
	if o.profileName != "" {
		if err := o.verifyIdentity(command.Context(), client); err != nil {
			return nil, "", err
		}
	}
	return client, source, nil
}

// checkOrganization prevents a supplied token from changing the selected team's identity.
func (o *options) checkOrganization(identity api.Identity) error {
	if identity.OrganizationID == "" {
		return errors.New("API identity is missing its organization")
	}
	profile, exists := o.preferences.Profiles[o.profileName]
	if exists && identity.OrganizationID != profile.OrganizationID {
		return fmt.Errorf("token is for team %q, but profile %q is bound to team %q; run sliplane auth login to switch teams or select a matching --profile", o.teamName(identity, identity.OrganizationID), o.profileName, o.teamName(identity, profile.OrganizationID))
	}
	return nil
}

func (o *options) teamName(identity api.Identity, id string) string {
	if name := identity.OrganizationName(id); name != "" {
		return name
	}
	for _, profile := range o.preferences.Profiles {
		if profile.OrganizationID == id && profile.OrganizationName != "" {
			return profile.OrganizationName
		}
	}
	return "name unavailable (log in through your browser to refresh)"
}

func (o *options) verifyIdentity(ctx context.Context, client *api.Client) error {
	identity, err := client.Me(ctx)
	if err != nil {
		return err
	}
	if err := o.checkOrganization(identity); err != nil {
		return err
	}
	o.identity = &identity
	return nil
}
