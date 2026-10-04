package cli

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"sliplane-cli/internal/api"
)

func (o *options) oauthClientCommand() *cobra.Command {
	command := &cobra.Command{Use: "oauth-client", Short: "Inspect and update OAuth clients"}
	command.AddCommand(&cobra.Command{Use: "list", Aliases: []string{"ls"}, Short: "List OAuth clients", Args: validateArguments,
		RunE: func(command *cobra.Command, _ []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			clients, err := client.OAuthClients(command.Context())
			if err != nil {
				return err
			}
			sort.Slice(clients, func(i, j int) bool { return clients[i].Name < clients[j].Name })
			rows := [][]string{{"NAME", "ID", "REDIRECT URIS"}}
			for _, entry := range clients {
				rows = append(rows, []string{entry.Name, entry.ID, strings.Join(entry.RedirectURIs, ", ")})
			}
			return o.print(command, clients, rows)
		},
	}, &cobra.Command{Use: "show <name-or-id>", Short: "Show an OAuth client", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			entry, err := resolveOAuthClient(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			entry, err = client.OAuthClient(command.Context(), entry.ID)
			if err != nil {
				return err
			}
			return o.printOAuthClient(command, entry)
		},
	}, &cobra.Command{Use: "users <name-or-id>", Short: "List users who authorized an OAuth client", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			entry, err := resolveOAuthClient(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			users, err := client.OAuthClientUsers(command.Context(), entry.ID)
			if err != nil {
				return err
			}
			rows := [][]string{{"NAME", "EMAIL", "GRANTS", "LAST AUTHORIZED", "USER ID"}}
			for _, user := range users {
				rows = append(rows, []string{user.Name, user.Email, strconv.Itoa(user.GrantCount), formatTimestamp(user.LastAuthorizedAt), user.UserID})
			}
			return o.print(command, users, rows)
		},
	}, o.oauthClientUpdateCommand())
	return command
}

func (o *options) oauthClientUpdateCommand() *cobra.Command {
	var name, imageURL string
	var redirectURIs []string
	command := &cobra.Command{Use: "update <name-or-id>", Short: "Update OAuth client settings (requires a read-write token)", Args: validateArguments,
		RunE: func(command *cobra.Command, args []string) error {
			input := api.UpdateOAuthClientInput{}
			if command.Flags().Changed("name") {
				if strings.TrimSpace(name) == "" {
					return errors.New("--name must not be empty")
				}
				input.Name = &name
			}
			if command.Flags().Changed("image-url") {
				input.ImageURL = &imageURL
			}
			if command.Flags().Changed("redirect-uri") {
				for _, value := range redirectURIs {
					uri, err := url.Parse(value)
					if err != nil || uri.Scheme == "" {
						return errors.New("--redirect-uri must be an absolute URI")
					}
				}
				input.RedirectURIs = &redirectURIs
			}
			if input.Name == nil && input.ImageURL == nil && input.RedirectURIs == nil {
				return errors.New("provide --name, --image-url, or --redirect-uri")
			}
			client, _, err := o.client(command)
			if err != nil {
				return err
			}
			entry, err := resolveOAuthClient(command.Context(), client, args[0])
			if err != nil {
				return err
			}
			entry, err = client.UpdateOAuthClient(command.Context(), entry.ID, input)
			if err != nil {
				return err
			}
			return o.printOAuthClient(command, entry)
		},
	}
	command.Flags().StringVar(&name, "name", "", "New display name")
	command.Flags().StringVar(&imageURL, "image-url", "", "Consent screen image URL (empty clears it)")
	command.Flags().StringArrayVar(&redirectURIs, "redirect-uri", nil, "Replace all redirect URIs (repeat for each URI)")
	return command
}

func resolveOAuthClient(ctx context.Context, client *api.Client, query string) (api.OAuthClient, error) {
	clients, err := client.OAuthClients(ctx)
	if err != nil {
		return api.OAuthClient{}, err
	}
	choices := make([]choice, len(clients))
	for i, client := range clients {
		choices[i] = choice{client.ID, client.Name}
	}
	index, err := resolve("OAuth client", query, choices)
	if err != nil {
		return api.OAuthClient{}, err
	}
	return clients[index], nil
}

func (o *options) printOAuthClient(command *cobra.Command, entry api.OAuthClient) error {
	return o.print(command, entry, [][]string{{"Name", entry.Name}, {"ID", entry.ID}, {"Organization", entry.OrganizationID}, {"Redirect URIs", strings.Join(entry.RedirectURIs, ", ")}, {"Image URL", entry.ImageURL}, {"Created", formatTimestamp(entry.CreatedAt)}, {"Updated", formatTimestamp(entry.UpdatedAt)}})
}
