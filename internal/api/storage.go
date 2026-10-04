package api

import (
	"context"
	"net/http"
	"net/url"
)

type CreateBucketInput struct {
	Name          string `json:"name"`
	Region        string `json:"region"`
	Versioning    bool   `json:"versioning"`
	ObjectLocking bool   `json:"objectLocking"`
}

func (c *Client) CreateBucket(ctx context.Context, input CreateBucketInput) (Bucket, error) {
	var bucket Bucket
	err := c.request(ctx, http.MethodPost, "/buckets", input, &bucket)
	return bucket, err
}

func (c *Client) Buckets(ctx context.Context) ([]Bucket, error) {
	return list[Bucket](ctx, c, "/buckets")
}

func (c *Client) PostgresDatabases(ctx context.Context) ([]PostgresDatabase, error) {
	return list[PostgresDatabase](ctx, c, "/postgres")
}

func (c *Client) PostgresDatabase(ctx context.Context, id string) (PostgresDatabase, error) {
	var database PostgresDatabase
	err := c.request(ctx, http.MethodGet, "/postgres/"+url.PathEscape(id), nil, &database)
	return database, err
}

// SetPostgresAllowList replaces the allowlist using only writable fields from each entry.
func (c *Client) SetPostgresAllowList(ctx context.Context, id string, entries []PostgresIPRange) error {
	rules := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		rules = append(rules, map[string]string{"source": entry.Source, "description": entry.Description})
	}
	return c.request(ctx, http.MethodPatch, "/postgres/"+url.PathEscape(id), map[string]any{"ipAllowList": rules}, nil)
}

func (c *Client) CreateBucketKey(ctx context.Context, bucketID, name string) (CreatedBucketKey, error) {
	var key CreatedBucketKey
	err := c.request(ctx, http.MethodPost, "/buckets/"+url.PathEscape(bucketID)+"/keys", map[string]string{"name": name}, &key)
	return key, err
}

func (c *Client) BucketKeys(ctx context.Context, bucketID string) ([]BucketKey, error) {
	return list[BucketKey](ctx, c, "/buckets/"+url.PathEscape(bucketID)+"/keys")
}

func (c *Client) DeleteBucketKey(ctx context.Context, bucketID, keyID string) error {
	return c.request(ctx, http.MethodDelete, "/buckets/"+url.PathEscape(bucketID)+"/keys/"+url.PathEscape(keyID), nil, nil)
}
