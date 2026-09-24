// Client uses only documented public endpoints, with bounded requests and no write retries.
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

// Client owns one token and refuses redirects so credentials cannot leave the API host.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// Error preserves the API status and machine-readable code without exposing credentials.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error adds actionable guidance for authentication, permissions, and rate limits.
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

// New creates a client; alternate URLs exist for local HTTP tests only.
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

// Me verifies the token and returns its effective access level.
func (c *Client) Me(ctx context.Context) (Identity, error) {
	var identity Identity
	err := c.request(ctx, http.MethodGet, "/me", nil, &identity)
	return identity, err
}

// Projects lists every project visible to the token's organization.
func (c *Client) Projects(ctx context.Context) ([]Project, error) {
	projects := []Project{}
	err := c.request(ctx, http.MethodGet, "/projects", nil, &projects)
	if projects == nil {
		projects = []Project{}
	}
	return projects, err
}

// CreateProject creates a named project without an interactive confirmation.
func (c *Client) CreateProject(ctx context.Context, name string) (Project, error) {
	var project Project
	err := c.request(ctx, http.MethodPost, "/projects", map[string]string{"name": name}, &project)
	return project, err
}

// Servers lists the organization's servers.
func (c *Client) Servers(ctx context.Context) ([]Server, error) {
	servers := []Server{}
	err := c.request(ctx, http.MethodGet, "/servers", nil, &servers)
	if servers == nil {
		servers = []Server{}
	}
	return servers, err
}

// Server reads current details after name resolution.
func (c *Client) Server(ctx context.Context, id string) (Server, error) {
	var server Server
	err := c.request(ctx, http.MethodGet, "/servers/"+url.PathEscape(id), nil, &server)
	return server, err
}

// Services lists services within one project.
func (c *Client) Services(ctx context.Context, projectID string) ([]Service, error) {
	services := []Service{}
	err := c.request(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/services", nil, &services)
	if services == nil {
		services = []Service{}
	}
	return services, err
}

// servicePath escapes both identifiers before composing a resource URL.
func servicePath(projectID, serviceID string) string {
	return "/projects/" + url.PathEscape(projectID) + "/services/" + url.PathEscape(serviceID)
}

// Service fetches details for a resolved service.
func (c *Client) Service(ctx context.Context, projectID, serviceID string) (Service, error) {
	var service Service
	err := c.request(ctx, http.MethodGet, servicePath(projectID, serviceID), nil, &service)
	return service, err
}

// Logs reads at most 500 lines, using the documented Unix-second lower bound.
func (c *Client) Logs(ctx context.Context, projectID, serviceID string, from *time.Time) ([]Log, error) {
	path := servicePath(projectID, serviceID) + "/logs"
	if from != nil {
		path += fmt.Sprintf("?from=%d", from.Unix())
	}
	logs := []Log{}
	err := c.request(ctx, http.MethodGet, path, nil, &logs)
	if logs == nil {
		logs = []Log{}
	}
	return logs, err
}
