package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"

	"charm.land/huh/v2"
	"github.com/sliplane/cli/internal/api"
	"github.com/sliplane/cli/internal/auth"
	"github.com/sliplane/cli/internal/config"
	"github.com/spf13/cobra"
)

func (o *options) authCommand() *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage authentication"}
	command.AddCommand(&cobra.Command{
		Use: "login", Short: "Log in through your browser or save an API token", Args: validateArguments,
		Long:    "Log in through a local browser and choose the Sliplane team to authorize there.\nSign in to sliplane.io first. When switching teams, choose to overwrite the current profile,\ncreate a new profile, or cancel. Overwriting clears the old team's project and S3 credentials.\nBrowser logins renew automatically while you use the CLI at least once every 7 days.\nSupply --token or SLIPLANE_TOKEN to save an API token instead.",
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
			return o.print(command, map[string]any{"profile": o.profileName, "source": source, "identity": identity}, [][]string{{"Profile", profileName}, {"Team", o.teamName(*identity, identity.OrganizationID)}, {"Access", identity.TokenType}, {"Auth", identity.AuthType}, {"Token source", source}})
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
	credential, err := o.loginToken(command)
	if err != nil {
		return err
	}
	identity, err := api.New(o.baseURL, credential.AccessToken).Me(command.Context())
	if err != nil {
		return err
	}
	if err := o.chooseLoginProfile(command, identity); err != nil {
		return err
	}
	if err := o.saveLogin(credential, identity); err != nil {
		return err
	}
	return o.print(command, map[string]any{"profile": o.profileName, "stored": true, "identity": identity}, [][]string{{"Profile", o.profileName}, {"Team", o.teamName(identity, identity.OrganizationID)}, {"Access", identity.TokenType}, {"Token saved to OS keychain."}})
}

func (o *options) chooseLoginProfile(command *cobra.Command, identity api.Identity) error {
	if identity.OrganizationID == "" {
		return errors.New("API identity is missing its organization")
	}
	profile, exists := o.preferences.Profiles[o.profileName]
	if !exists || profile.OrganizationID == identity.OrganizationID {
		return nil
	}
	if !o.canPrompt(command) {
		return fmt.Errorf("profile %q is bound to team %q; login is for team %q. Rerun with --output text in a terminal to overwrite, create a profile, or cancel; or supply --profile with a new name", o.profileName, o.teamName(identity, profile.OrganizationID), o.teamName(identity, identity.OrganizationID))
	}
	action := ""
	field := huh.NewSelect[string]().Title("Switch teams").Description(fmt.Sprintf("Profile %q uses %q. You signed in to %q.", o.profileName, terminalText(o.teamName(identity, profile.OrganizationID)), terminalText(o.teamName(identity, identity.OrganizationID)))).Options(
		huh.NewOption("Overwrite current profile", "overwrite"),
		huh.NewOption("Create a new profile", "create"),
		huh.NewOption("Cancel", "cancel"),
	).Value(&action)
	if err := runPrompt(command, field); err != nil {
		return err
	}
	switch action {
	case "overwrite":
		return nil
	case "create":
		var name string
		field := huh.NewInput().Title("New profile name").Value(&name).Validate(func(value string) error {
			if err := config.ValidateProfileName(value); err != nil {
				return err
			}
			if _, exists := o.preferences.Profiles[value]; exists {
				return fmt.Errorf("profile %q already exists; choose a new name", value)
			}
			return nil
		})
		if err := runPrompt(command, field); err != nil {
			return err
		}
		o.profileName = name
		o.preferences.ActiveProfile = name
		return nil
	default:
		return context.Canceled
	}
}

var oauthClient = auth.OAuthClient{
	ID:               "oclient_dj5pznsma6kt",
	AuthorizationURL: "https://api.sliplane.io/web/oauth/authorize",
	TokenURL:         "https://api.sliplane.io/web/oauth/token",
}

// loginToken opens the browser unless a token was explicitly supplied.
func (o *options) loginToken(command *cobra.Command) (auth.Credential, error) {
	if command.Flags().Changed("token") || os.Getenv("SLIPLANE_TOKEN") != "" {
		token, _, err := auth.Resolve(o.token, command.Flags().Changed("token"), os.Getenv("SLIPLANE_TOKEN"), "")
		return auth.Credential{AccessToken: token}, err
	}
	return oauthClient.Login(command.Context(), func(authorizationURL string) error {
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

// saveLogin preserves same-team settings; a team switch gets fresh credentials and settings.
func (o *options) saveLogin(credential auth.Credential, identity api.Identity) error {
	previous, exists := o.preferences.Profiles[o.profileName]
	preferences := o.preferences
	preferences.Profiles = maps.Clone(preferences.Profiles)
	if preferences.Profiles == nil {
		preferences.Profiles = map[string]config.Profile{}
	}
	// OAuth includes names for all accessible teams, including profiles saved by older versions.
	for name, profile := range preferences.Profiles {
		if team := identity.OrganizationName(profile.OrganizationID); team != "" {
			profile.OrganizationName = team
			preferences.Profiles[name] = profile
		}
	}
	if exists && previous.OrganizationID == identity.OrganizationID {
		if err := config.Save(o.configPath, preferences); err != nil {
			return err
		}
		if err := auth.SaveCredential(previous.CredentialID, credential); err != nil {
			return err
		}
		o.preferences = preferences
		return nil
	}
	bytes := make([]byte, 16)
	rand.Read(bytes)
	credentialID := "credential_" + hex.EncodeToString(bytes)
	if err := auth.SaveCredential(credentialID, credential); err != nil {
		return err
	}
	preferences.Profiles[o.profileName] = config.Profile{OrganizationID: identity.OrganizationID, OrganizationName: identity.OrganizationName(identity.OrganizationID), CredentialID: credentialID}
	if len(preferences.Profiles) == 1 {
		preferences.ActiveProfile = o.profileName
	}
	if err := config.Save(o.configPath, preferences); err != nil {
		return errors.Join(err, auth.Delete(credentialID))
	}
	o.preferences = preferences
	if exists {
		cleanupErr := auth.Delete(previous.CredentialID)
		for _, reference := range previous.S3Credentials {
			cleanupErr = errors.Join(cleanupErr, auth.Delete(reference))
		}
		if cleanupErr != nil {
			return fmt.Errorf("login saved, but could not remove the previous team's saved credentials: %w", cleanupErr)
		}
	}
	return nil
}

// refreshLogin rotates a saved browser login, reusing a pair another process already rotated.
func refreshLogin(credentialID string) func(context.Context, string) (string, error) {
	return func(ctx context.Context, rejected string) (string, error) {
		credential, err := auth.LoadCredential(credentialID)
		if err != nil {
			return "", err
		}
		if credential.AccessToken != rejected {
			return credential.AccessToken, nil
		}
		refreshed, err := oauthClient.Refresh(ctx, credential.RefreshToken)
		if err != nil {
			if current, loadErr := auth.LoadCredential(credentialID); loadErr == nil && current.AccessToken != rejected {
				return current.AccessToken, nil
			}
			return "", err
		}
		if err := auth.SaveCredential(credentialID, refreshed); err != nil {
			return "", err
		}
		return refreshed.AccessToken, nil
	}
}
