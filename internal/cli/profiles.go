package cli

import (
	"fmt"
	"sort"

	"github.com/sliplane/cli/internal/api"
	"github.com/sliplane/cli/internal/auth"
	"github.com/sliplane/cli/internal/config"
	"github.com/spf13/cobra"
)

type profileSummary struct {
	Name             string `json:"name"`
	Active           bool   `json:"active"`
	Default          bool   `json:"default"`
	OrganizationID   string `json:"organizationId"`
	OrganizationName string `json:"organizationName,omitempty"`
	Project          string `json:"project,omitempty"`
}

func (o *options) profileCommand() *cobra.Command {
	command := &cobra.Command{Use: "profile", Aliases: []string{"profiles"}, Short: "Manage team profiles"}
	command.AddCommand(&cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List saved profiles", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			profiles := []profileSummary{}
			for name, profile := range o.preferences.Profiles {
				profiles = append(profiles, profileSummary{Name: name, Active: name == o.profileName, Default: name == o.preferences.ActiveProfile, OrganizationID: profile.OrganizationID, OrganizationName: profile.OrganizationName, Project: profile.Project})
			}
			sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
			rows := [][]string{{"NAME", "ACTIVE", "TEAM", "PROJECT"}}
			for _, profile := range profiles {
				marker := ""
				if profile.Active {
					marker = "*"
				}
				rows = append(rows, []string{profile.Name, marker, o.teamName(api.Identity{}, profile.OrganizationID), profile.Project})
			}
			return o.print(command, profiles, rows)
		},
	}, &cobra.Command{
		Use: "use <name>", Short: "Set the default profile", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			if _, exists := o.preferences.Profiles[args[0]]; !exists {
				return fmt.Errorf("profile %q not found", args[0])
			}
			preferences := o.preferences
			preferences.ActiveProfile = args[0]
			if err := config.Save(o.configPath, preferences); err != nil {
				return err
			}
			return o.print(command, map[string]string{"activeProfile": args[0]}, [][]string{{"Default profile", args[0]}})
		},
	}, &cobra.Command{
		Use: "remove <name>", Short: "Remove a profile and its saved token", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			profile, exists := o.preferences.Profiles[args[0]]
			if !exists {
				return fmt.Errorf("profile %q not found", args[0])
			}
			for _, reference := range profile.S3Credentials {
				if err := auth.Delete(reference); err != nil {
					return err
				}
			}
			if err := auth.Delete(profile.CredentialID); err != nil {
				return err
			}
			preferences := o.preferences
			delete(preferences.Profiles, args[0])
			if preferences.ActiveProfile == args[0] {
				preferences.ActiveProfile = ""
			}
			if err := config.Save(o.configPath, preferences); err != nil {
				return fmt.Errorf("token removed, but profile settings could not be saved: %w", err)
			}
			return o.print(command, map[string]any{"profile": args[0], "removed": true}, [][]string{{"Profile removed", args[0]}})
		},
	})
	return command
}
