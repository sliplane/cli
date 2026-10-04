// Bucket management uses the control API without requiring S3 access keys.
package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type BucketDeletion struct {
	Message   string    `json:"message"`
	DeletesAt time.Time `json:"deletesAt"`
}

type BucketCORS struct {
	Rules []BucketCORSRule `json:"rules"`
}

type BucketCORSRule struct {
	AllowedOrigins []string `json:"allowedOrigins"`
	AllowedMethods []string `json:"allowedMethods"`
	AllowedHeaders []string `json:"allowedHeaders,omitempty"`
	ExposeHeaders  []string `json:"exposeHeaders,omitempty"`
	MaxAgeSeconds  *int     `json:"maxAgeSeconds,omitempty"`
}

func (c *Client) DeleteBucket(ctx context.Context, id string) (BucketDeletion, error) {
	var deletion BucketDeletion
	err := c.request(ctx, http.MethodDelete, "/buckets/"+url.PathEscape(id), nil, &deletion)
	return deletion, err
}

func (c *Client) SetBucketVersioning(ctx context.Context, id string, enabled bool) (Bucket, error) {
	var bucket Bucket
	err := c.request(ctx, http.MethodPatch, "/buckets/"+url.PathEscape(id), map[string]bool{"versioning": enabled}, &bucket)
	return bucket, err
}

func (c *Client) BucketCORS(ctx context.Context, id string) (BucketCORS, error) {
	var cors BucketCORS
	err := c.request(ctx, http.MethodGet, "/buckets/"+url.PathEscape(id)+"/cors", nil, &cors)
	return cors, err
}

func (c *Client) SetBucketCORS(ctx context.Context, id string, input BucketCORS) (BucketCORS, error) {
	var cors BucketCORS
	err := c.request(ctx, http.MethodPut, "/buckets/"+url.PathEscape(id)+"/cors", input, &cors)
	return cors, err
}

func (c *Client) DeleteBucketCORS(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/buckets/"+url.PathEscape(id)+"/cors", nil, nil)
}
