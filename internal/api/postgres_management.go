// Database management uses public PostgreSQL endpoints and never retries mutations.
package api

import (
	"context"
	"net/http"
	"net/url"
)

// CreatePostgresInput leaves optional sizing and database identifiers to the API defaults.
type CreatePostgresInput struct {
	Name         string `json:"name"`
	Region       string `json:"region"`
	InstanceType string `json:"instanceType"`
	BillingCycle string `json:"billingCycle,omitempty"`
	DiskSizeGB   int    `json:"diskSizeGb,omitempty"`
	Version      int    `json:"version,omitempty"`
	DatabaseName string `json:"databaseName,omitempty"`
	DatabaseUser string `json:"databaseUser,omitempty"`
}

// UpdatePostgresInput sends only fields explicitly selected by the caller.
type UpdatePostgresInput struct {
	Name         *string `json:"name,omitempty"`
	InstanceType *string `json:"instanceType,omitempty"`
	DiskSizeGB   *int    `json:"diskSizeGb,omitempty"`
}

// CreatePostgres requests provisioning and returns its current status, not a readiness guarantee.
func (c *Client) CreatePostgres(ctx context.Context, input CreatePostgresInput) (PostgresDatabase, error) {
	var database PostgresDatabase
	err := c.request(ctx, http.MethodPost, "/postgres", input, &database)
	return database, err
}

// UpdatePostgres renames or resizes a database without replacing its access rules.
func (c *Client) UpdatePostgres(ctx context.Context, id string, input UpdatePostgresInput) (PostgresDatabase, error) {
	var database PostgresDatabase
	err := c.request(ctx, http.MethodPatch, "/postgres/"+url.PathEscape(id), input, &database)
	return database, err
}

// DeletePostgres permanently deletes the explicitly resolved database.
func (c *Client) DeletePostgres(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/postgres/"+url.PathEscape(id), nil, nil)
}

// PostgresAction runs a fixed lifecycle or credential action supplied by the command tree.
func (c *Client) PostgresAction(ctx context.Context, id, action string) (PostgresDatabase, error) {
	var database PostgresDatabase
	err := c.request(ctx, http.MethodPost, "/postgres/"+url.PathEscape(id)+"/"+action, nil, &database)
	return database, err
}
