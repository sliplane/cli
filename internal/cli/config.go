// Output preferences are shared, while each profile owns its default project.
package cli

import (
	"errors"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/config"
)

// configCommand offers noninteractive preference inspection and editing.
func (o *options) configCommand() *cobra.Command {
	command := &cobra.Command{Use: "config", Short: "Manage local preferences"}
	command.AddCommand(&cobra.Command{
		Use: "show", Short: "Show saved preferences and their file path", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			preferences, err := config.Load(o.configPath)
			if err != nil {
				return err
			}
			return o.print(command, map[string]any{"path": o.configPath, "preferences": preferences}, [][]string{{"File", o.configPath}, {"Profile", o.profileName}, {"Project", preferences.Profiles[o.profileName].Project}, {"Output", preferences.Output}})
		},
	}, &cobra.Command{
		Use: "set <project|output> <value>", Short: "Save a default project or output format", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error { return o.setPreference(command, args[0], args[1]) },
	}, &cobra.Command{
		Use: "unset <project|output>", Short: "Remove a saved preference", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error { return o.setPreference(command, args[0], "") },
	})
	return command
}

// setPreference validates the key before atomically updating the existing preferences.
func (o *options) setPreference(command *cobra.Command, key, value string) error {
	preferences, err := config.Load(o.configPath)
	if err != nil {
		return err
	}
	switch key {
	case "project":
		profile, exists := preferences.Profiles[o.profileName]
		if !exists {
			return errors.New("select a profile before setting its default project; use --project for a single command")
		}
		profile.Project = value
		preferences.Profiles[o.profileName] = profile
	case "output":
		if value != "" && value != "text" && value != "json" {
			return errors.New("output must be text or json")
		}
		preferences.Output = value
	default:
		return errors.New("config supports only project and output; tokens belong in the OS keychain")
	}
	if err := config.Save(o.configPath, preferences); err != nil {
		return err
	}
	return o.print(command, preferences, [][]string{{"Profile", o.profileName}, {"Project", preferences.Profiles[o.profileName].Project}, {"Output", preferences.Output}})
}
