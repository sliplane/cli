// Authentication verifies team identity before storing or using a profile credential.
package cli

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
	"sliplane-cli/internal/auth"
	"sliplane-cli/internal/config"
)

// authCommand exposes login, verified status, and local credential removal.
func (o *options) authCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage API token authentication"}
	command.AddCommand(&cobra.Command{
		Use: "login", Short: "Save a token for a profile", Args: validateArguments,
		Example: "  sliplane auth login --profile work --token TOKEN",
		RunE:    func(command *cobra.Command, _ []string) error { return o.login(command) },
	}, &cobra.Command{
		Use: "status", Short: "Show the current profile, team, and token access", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, source, err := o.client(command)
			if err != nil {
				return err
			}
			identity := o.identity
			if identity == nil {
				verified, err := client.Me(command.Context())
				if err != nil {
					return err
				}
				if err := o.checkOrganization(verified); err != nil {
					return err
				}
				identity = &verified
			}
			profileName := o.profileName
			if profileName == "" {
				profileName = "(none)"
			}
			return o.print(command, map[string]any{"profile": o.profileName, "source": source, "identity": identity}, [][]string{{"Profile", profileName}, {"Organization", identity.OrganizationID}, {"Access", identity.TokenType}, {"Auth", identity.AuthType}, {"Token source", source}})
		},
	}, &cobra.Command{
		Use: "logout", Short: "Remove the selected profile's saved token", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := auth.Delete(o.credentialID()); err != nil {
				return err
			}
			return o.print(command, map[string]any{"profile": o.profileName, "removed": true}, [][]string{{"Saved keychain token removed. Flags and environment are unaffected."}})
		},
	})
	return command
}

// login creates a profile on first use and refuses to rebind an existing profile to another team.
func (o *options) login(command *cobra.Command) error {
	credentialID := o.credentialID()
	if o.profileName == "" {
		o.profileName = "default"
		if profile, exists := o.preferences.Profiles[o.profileName]; exists {
			credentialID = profile.CredentialID
		}
	}
	token, source, err := auth.Resolve(o.token, command.Flags().Changed("token"), os.Getenv("SLIPLANE_TOKEN"), credentialID)
	if err != nil {
		return err
	}
	identity, err := api.New(o.baseURL, token).Me(command.Context())
	if err != nil {
		return err
	}
	if err := o.checkOrganization(identity); err != nil {
		return err
	}
	if err := o.saveLogin(token, identity); err != nil {
		return err
	}
	if source == "keychain" && credentialID == auth.LegacyCredentialID {
		if err := auth.Delete(auth.LegacyCredentialID); err != nil {
			return fmt.Errorf("profile saved, but could not remove the old credential: %w", err)
		}
	}
	return o.print(command, map[string]any{"profile": o.profileName, "stored": true, "identity": identity}, [][]string{{"Profile", o.profileName}, {"Organization", identity.OrganizationID}, {"Access", identity.TokenType}, {"Token saved to OS keychain."}})
}

// saveLogin keeps each profile's keychain reference stable when its token is rotated.
func (o *options) saveLogin(token string, identity api.Identity) error {
	if profile, exists := o.preferences.Profiles[o.profileName]; exists {
		return auth.Save(profile.CredentialID, token)
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Errorf("create credential reference: %w", err)
	}
	credentialID := "credential_" + hex.EncodeToString(bytes)
	if err := auth.Save(credentialID, token); err != nil {
		return err
	}
	preferences := o.preferences
	if preferences.Profiles == nil {
		preferences.Profiles = map[string]config.Profile{}
	}
	preferences.Profiles[o.profileName] = config.Profile{OrganizationID: identity.OrganizationID, CredentialID: credentialID, Project: preferences.Project}
	preferences.Project = ""
	preferences.Version = 1
	if len(preferences.Profiles) == 1 {
		preferences.ActiveProfile = o.profileName
	}
	if err := config.Save(o.configPath, preferences); err != nil {
		return errors.Join(err, auth.Delete(credentialID))
	}
	return nil
}
