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

func (o *options) authCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage authentication"}
	command.AddCommand(&cobra.Command{
		Use: "login", Short: "Log in through your browser or save an API token", Args: validateArguments,
		Long:    "Log in through a local browser using its active Sliplane team. Sign in to\nsliplane.io first. Browser tokens expire after 24 hours; run login again to renew.\nSupply --token or SLIPLANE_TOKEN to save an API token instead.",
		Example: "  sliplane auth login\n  sliplane auth login --profile work --token TOKEN",
		RunE:    func(command *cobra.Command, _ []string) error { return o.login(command) },
	}, &cobra.Command{
		Use: "status", Short: "Show the current profile, team, and token access", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, source, err := o.client(command)
			if err != nil {
				return err
			}
			if o.identity == nil {
				if err := o.verifyIdentity(command.Context(), client); err != nil {
					return err
				}
			}
			identity := o.identity
			profileName := o.profileName
			if profileName == "" {
				profileName = "(none)"
			}
			return o.print(command, map[string]any{"profile": o.profileName, "source": source, "identity": identity}, [][]string{{"Profile", profileName}, {"Organization", identity.OrganizationID}, {"Access", identity.TokenType}, {"Auth", identity.AuthType}, {"Token source", source}})
		},
	}, &cobra.Command{
		Use: "logout", Short: "Remove the selected profile's saved token", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := auth.Delete(o.preferences.Profiles[o.profileName].CredentialID); err != nil {
				return err
			}
			return o.print(command, map[string]any{"profile": o.profileName, "removed": true}, [][]string{{"Saved keychain token removed. Flags and environment are unaffected."}})
		},
	})
	return command
}

func (o *options) login(command *cobra.Command) error {
	if o.profileName == "" {
		o.profileName = "default"
	}
	token, err := o.loginToken(command)
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
	return o.print(command, map[string]any{"profile": o.profileName, "stored": true, "identity": identity}, [][]string{{"Profile", o.profileName}, {"Organization", identity.OrganizationID}, {"Access", identity.TokenType}, {"Token saved to OS keychain."}})
}

// loginToken opens the browser unless a token was explicitly supplied.
func (o *options) loginToken(command *cobra.Command) (string, error) {
	if command.Flags().Changed("token") || os.Getenv("SLIPLANE_TOKEN") != "" {
		token, _, err := auth.Resolve(o.token, command.Flags().Changed("token"), os.Getenv("SLIPLANE_TOKEN"), "")
		return token, err
	}
	client := auth.OAuthClient{
		ID:               "oclient_dj5pznsma6kt",
		AuthorizationURL: "https://api.sliplane.io/web/oauth/authorize",
		TokenURL:         "https://api.sliplane.io/web/oauth/token",
	}
	return client.Login(command.Context(), func(authorizationURL string) error {
		fmt.Fprintln(command.ErrOrStderr(), "Opening Sliplane in your browser. Waiting for approval (up to 5 minutes); press Ctrl+C to cancel.")
		if err := openBrowser(command.Context(), authorizationURL); err != nil {
			if command.Context().Err() != nil {
				return command.Context().Err()
			}
			fmt.Fprintf(command.ErrOrStderr(), "Could not open your browser. Open this URL on this computer:\n%s\n", authorizationURL)
		}
		return nil
	})
}

// saveLogin keeps each profile's keychain reference stable when its token is rotated.
func (o *options) saveLogin(token string, identity api.Identity) error {
	if profile, exists := o.preferences.Profiles[o.profileName]; exists {
		return auth.Save(profile.CredentialID, token)
	}
	bytes := make([]byte, 16)
	rand.Read(bytes)
	credentialID := "credential_" + hex.EncodeToString(bytes)
	if err := auth.Save(credentialID, token); err != nil {
		return err
	}
	preferences := o.preferences
	if preferences.Profiles == nil {
		preferences.Profiles = map[string]config.Profile{}
	}
	preferences.Profiles[o.profileName] = config.Profile{OrganizationID: identity.OrganizationID, CredentialID: credentialID}
	if len(preferences.Profiles) == 1 {
		preferences.ActiveProfile = o.profileName
	}
	if err := config.Save(o.configPath, preferences); err != nil {
		return errors.Join(err, auth.Delete(credentialID))
	}
	return nil
}
