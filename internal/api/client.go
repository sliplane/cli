package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"sliplane-cli/internal/buildinfo"
)

const BaseURL = "https://ctrl.sliplane.io/v0"

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	message := fmt.Sprintf("API request failed (HTTP %d)", e.Status)
	if e.Code != "" {
		message += " [" + e.Code + "]"
	}
	if e.Message != "" {
		message += ": " + e.Message
	}
	switch e.Status {
	case http.StatusUnauthorized:
		message += "; supply a valid --token or SLIPLANE_TOKEN, or run sliplane auth login"
	case http.StatusTooManyRequests:
		message += "; wait before retrying"
	}
	if e.Code == "read_only_token" {
		message += "; this command requires a read-write token"
	}
	return message
}

func New(baseURL, token string) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), token: token, http: &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// request sends one authenticated request and never prints transport or response bodies.
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode API request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "sliplane-cli/"+buildinfo.Version)
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("API request failed: %s", strings.ReplaceAll(err.Error(), c.token, "[redacted]"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiError := &Error{Status: response.StatusCode}
		// Gateways may return HTML; only structured errors are suitable for display.
		_ = json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(apiError)
		apiError.Message = strings.ReplaceAll(apiError.Message, c.token, "[redacted]")
		apiError.Code = strings.ReplaceAll(apiError.Code, c.token, "[redacted]")
		return apiError
	}
	if output == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}
	return nil
}

func (c *Client) Me(ctx context.Context) (Identity, error) {
	var identity Identity
	err := c.request(ctx, http.MethodGet, "/me", nil, &identity)
	return identity, err
}

func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	return list[Project](ctx, c, "/projects")
}

func (c *Client) CreateProject(ctx context.Context, name string) (Project, error) {
	var project Project
	err := c.request(ctx, http.MethodPost, "/projects", map[string]string{"name": name}, &project)
	return project, err
}

func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	return list[Server](ctx, c, "/servers")
}

func (c *Client) Server(ctx context.Context, id string) (Server, error) {
	var server Server
	err := c.request(ctx, http.MethodGet, "/servers/"+url.PathEscape(id), nil, &server)
	return server, err
}

func (c *Client) Services(ctx context.Context, projectID string) ([]Service, error) {
	return list[Service](ctx, c, "/projects/"+url.PathEscape(projectID)+"/services")
}

func servicePath(projectID, serviceID string) string {
	return "/projects/" + url.PathEscape(projectID) + "/services/" + url.PathEscape(serviceID)
}

func (c *Client) Service(ctx context.Context, projectID, serviceID string) (Service, error) {
	var service Service
	err := c.request(ctx, http.MethodGet, servicePath(projectID, serviceID), nil, &service)
	return service, err
}

func (c *Client) Logs(ctx context.Context, projectID, serviceID string, from *time.Time) ([]Log, error) {
	path := servicePath(projectID, serviceID) + "/logs"
	if from != nil {
		path += fmt.Sprintf("?from=%d", from.Unix())
	}
	return list[Log](ctx, c, path)
}

func list[T any](ctx context.Context, client *Client, path string) ([]T, error) {
	items := []T{}
	err := client.request(ctx, http.MethodGet, path, nil, &items)
	if items == nil {
		items = []T{}
	}
	return items, err
}
