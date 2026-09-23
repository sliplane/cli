// Commands share output preferences and resolve credentials only when calling the API.
package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
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
}

// NewCommand constructs an independent command tree for each invocation.
func NewCommand() *cobra.Command { return newCommand(api.BaseURL) }

// newCommand accepts a local HTTP test server without exposing an endpoint override to users.
func newCommand(baseURL string) *cobra.Command {
	options := &options{baseURL: baseURL}
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
	root.AddCommand(options.versionCommand(), options.agentCommand(), options.authCommand(), options.profileCommand(), options.configCommand(), options.projectCommand(), options.serverCommand(), options.serviceCommand(), options.s3Command(), options.postgresCommand(), options.logsCommand(), options.deployCommand(), options.sshCommand())
	return root
}

// load applies file preferences unless an explicit flag overrides them.
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
		if o.profileName == "" && preferences.Version == 0 {
			o.project = preferences.Project
		}
	}
	if o.output != "text" && o.output != "json" {
		return errors.New("--output must be text or json")
	}
	return nil
}

// client resolves credentials without caching or persisting supplied tokens.
func (o *options) client(command *cobra.Command) (*api.Client, string, error) {
	token, source, err := auth.Resolve(o.token, command.Flags().Changed("token"), os.Getenv("SLIPLANE_TOKEN"), o.credentialID())
	if err != nil {
		return nil, "", err
	}
	client := api.New(o.baseURL, token)
	if o.profileName != "" {
		identity, err := client.Me(command.Context())
		if err != nil {
			return nil, "", err
		}
		if err := o.checkOrganization(identity); err != nil {
			return nil, "", err
		}
		o.identity = &identity
	}
	return client, source, nil
}

// credentialID never falls back to another profile when a selected credential is missing.
func (o *options) credentialID() string {
	if o.profileName != "" {
		return o.preferences.Profiles[o.profileName].CredentialID
	}
	if o.preferences.Version == 0 {
		return auth.LegacyCredentialID
	}
	return ""
}

// checkOrganization prevents a supplied token from changing the selected team's identity.
func (o *options) checkOrganization(identity api.Identity) error {
	if identity.OrganizationID == "" {
		return errors.New("API identity is missing its organization")
	}
	profile, exists := o.preferences.Profiles[o.profileName]
	if exists && identity.OrganizationID != profile.OrganizationID {
		return fmt.Errorf("token belongs to organization %q, but profile %q is bound to %q; select the matching profile or supply a token for this team", identity.OrganizationID, o.profileName, profile.OrganizationID)
	}
	return nil
}
