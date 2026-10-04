package api

import (
	"context"
	"net/http"
	"net/url"
)

// RegistryCredentials intentionally excludes the write-only registry token.
type RegistryCredentials struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Username  string `json:"username"`
	CreatedAt string `json:"createdAt"`
}

type CreateRegistryCredentialsInput struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Username string `json:"username"`
	Token    string `json:"token"`
}

func (c *Client) RegistryCredentials(ctx context.Context) ([]RegistryCredentials, error) {
	return list[RegistryCredentials](ctx, c, "/registry-credentials")
}

func (c *Client) RegistryCredential(ctx context.Context, id string) (RegistryCredentials, error) {
	var credentials RegistryCredentials
	err := c.request(ctx, http.MethodGet, "/registry-credentials/"+url.PathEscape(id), nil, &credentials)
	return credentials, err
}

func (c *Client) CreateRegistryCredentials(ctx context.Context, input CreateRegistryCredentialsInput) (RegistryCredentials, error) {
	var credentials RegistryCredentials
	err := c.request(ctx, http.MethodPost, "/registry-credentials", input, &credentials)
	return credentials, err
}

func (c *Client) RenameRegistryCredentials(ctx context.Context, id, name string) (RegistryCredentials, error) {
	var credentials RegistryCredentials
	err := c.request(ctx, http.MethodPatch, "/registry-credentials/"+url.PathEscape(id), map[string]string{"name": name}, &credentials)
	return credentials, err
}

func (c *Client) DeleteRegistryCredentials(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/registry-credentials/"+url.PathEscape(id), nil, nil)
}

type OAuthClient struct {
	ID             string   `json:"id"`
	OrganizationID string   `json:"organizationId"`
	Name           string   `json:"name"`
	ImageURL       string   `json:"imageUrl,omitempty"`
	RedirectURIs   []string `json:"redirectUris"`
	CreatedAt      string   `json:"createdAt"`
	UpdatedAt      string   `json:"updatedAt"`
}

type OAuthClientUser struct {
	UserID            string `json:"userId"`
	Name              string `json:"name"`
	Email             string `json:"email"`
	AvatarURL         string `json:"avatarUrl"`
	FirstAuthorizedAt string `json:"firstAuthorizedAt"`
	LastAuthorizedAt  string `json:"lastAuthorizedAt"`
	GrantCount        int    `json:"grantCount"`
}

type UpdateOAuthClientInput struct {
	Name         *string   `json:"name,omitempty"`
	ImageURL     *string   `json:"imageUrl,omitempty"`
	RedirectURIs *[]string `json:"redirectUris,omitempty"`
}

func (c *Client) OAuthClients(ctx context.Context) ([]OAuthClient, error) {
	return list[OAuthClient](ctx, c, "/oauth-clients")
}

func (c *Client) OAuthClient(ctx context.Context, id string) (OAuthClient, error) {
	var client OAuthClient
	err := c.request(ctx, http.MethodGet, "/oauth-clients/"+url.PathEscape(id), nil, &client)
	return client, err
}

func (c *Client) UpdateOAuthClient(ctx context.Context, id string, input UpdateOAuthClientInput) (OAuthClient, error) {
	var client OAuthClient
	err := c.request(ctx, http.MethodPatch, "/oauth-clients/"+url.PathEscape(id), input, &client)
	return client, err
}

func (c *Client) OAuthClientUsers(ctx context.Context, id string) ([]OAuthClientUser, error) {
	return list[OAuthClientUser](ctx, c, "/oauth-clients/"+url.PathEscape(id)+"/users")
}
