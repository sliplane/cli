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
