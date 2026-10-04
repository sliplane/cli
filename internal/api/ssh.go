package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type SSHConnection struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
}

type SSHKey struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	PublicKey   string    `json:"publicKey"`
	Fingerprint string    `json:"fingerprint"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (c *Client) CreateSSHKey(ctx context.Context, name, publicKey string, expiresAt time.Time) (SSHKey, error) {
	body := struct {
		Name      string     `json:"name"`
		PublicKey string     `json:"publicKey"`
		ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	}{Name: name, PublicKey: publicKey, ExpiresAt: nil}
	if !expiresAt.IsZero() {
		body.ExpiresAt = &expiresAt
	}
	var key SSHKey
	err := c.request(ctx, http.MethodPost, "/ssh-keys", body, &key)
	return key, err
}

func (c *Client) AttachSSHKey(ctx context.Context, projectID, serviceID, keyID string) (SSHConnection, error) {
	var connection SSHConnection
	err := c.request(ctx, http.MethodPut, servicePath(projectID, serviceID)+"/ssh-keys/"+url.PathEscape(keyID), nil, &connection)
	return connection, err
}

func (c *Client) DetachSSHKey(ctx context.Context, projectID, serviceID, keyID string) error {
	return c.request(ctx, http.MethodDelete, servicePath(projectID, serviceID)+"/ssh-keys/"+url.PathEscape(keyID), nil, nil)
}

func (c *Client) DeleteSSHKey(ctx context.Context, keyID string) error {
	return c.request(ctx, http.MethodDelete, "/ssh-keys/"+url.PathEscape(keyID), nil, nil)
}

func (c *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	return list[SSHKey](ctx, c, "/ssh-keys")
}

type ServiceSSHKey struct {
	KeyID       string    `json:"keyId"`
	Name        string    `json:"name"`
	Fingerprint string    `json:"fingerprint"`
	AttachedAt  time.Time `json:"attachedAt"`
}

func (c *Client) ServiceSSHKeys(ctx context.Context, projectID, serviceID string) ([]ServiceSSHKey, error) {
	return list[ServiceSSHKey](ctx, c, servicePath(projectID, serviceID)+"/ssh-keys")
}
