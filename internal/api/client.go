package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sliplane/cli/internal/buildinfo"
)

const BaseURL = "https://ctrl.sliplane.io/v0"

type Client struct {
	baseURL string
	http    *http.Client
	mutex   sync.Mutex
	token   string
	refresh func(ctx context.Context, rejected string) (string, error)
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

// SetRefresh lets the client replace an expired token once per rejected request.
func (c *Client) SetRefresh(refresh func(ctx context.Context, rejected string) (string, error)) {
	c.refresh = refresh
}

func (c *Client) currentToken() string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.token
}

// refreshToken lets concurrent requests share one refresh of the same rejected token.
func (c *Client) refreshToken(ctx context.Context, rejected string) (bool, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.refresh == nil {
		return false, nil
	}
	if c.token != rejected {
		return true, nil
	}
	token, err := c.refresh(ctx, rejected)
	if err != nil {
		return false, err
	}
	c.token = token
	return true, nil
}

// request sends one authenticated request and never prints transport or response bodies.
func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	_, err := c.requestHeaders(ctx, method, path, input, output)
	return err
}

func (c *Client) requestHeaders(ctx context.Context, method, path string, input, output any) (http.Header, error) {
	var encoded []byte
	if input != nil {
		var err error
		if encoded, err = json.Marshal(input); err != nil {
			return nil, fmt.Errorf("encode API request: %w", err)
		}
	}
	token := c.currentToken()
	header, err := c.send(ctx, method, path, encoded, token, output)
	var apiError *Error
	if errors.As(err, &apiError) && apiError.Status == http.StatusUnauthorized {
		refreshed, refreshErr := c.refreshToken(ctx, token)
		if refreshErr != nil {
			return nil, refreshErr
		}
		if refreshed {
			return c.send(ctx, method, path, encoded, c.currentToken(), output)
		}
	}
	return header, err
}

func (c *Client) send(ctx context.Context, method, path string, encoded []byte, token string, output any) (http.Header, error) {
	var body io.Reader
	if encoded != nil {
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("create API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "sliplane-cli/"+buildinfo.Version)
	if encoded != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("API request failed: %s", strings.ReplaceAll(err.Error(), token, "[redacted]"))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		apiError := &Error{Status: response.StatusCode}
		// Gateways may return HTML; only structured errors are suitable for display.
		_ = json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(apiError)
		apiError.Message = strings.ReplaceAll(apiError.Message, token, "[redacted]")
		apiError.Code = strings.ReplaceAll(apiError.Code, token, "[redacted]")
		return nil, apiError
	}
	if output == nil {
		return response.Header, nil
	}
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return nil, fmt.Errorf("decode API response: %w", err)
	}
	return response.Header, nil
}

func (c *Client) Me(ctx context.Context) (Identity, error) {
	var identity Identity
	err := c.request(ctx, http.MethodGet, "/me", nil, &identity)
	return identity, err
}

// ClientIP returns the public address the API saw over network ("tcp4" or "tcp6").
// Proxies are bypassed because their address is not the source of a direct database connection.
func (c *Client) ClientIP(ctx context.Context, network string) (netip.Addr, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	defer transport.CloseIdleConnections()
	direct := &Client{baseURL: c.baseURL, token: c.currentToken(), http: &http.Client{Transport: transport, Timeout: c.http.Timeout, CheckRedirect: c.http.CheckRedirect}}
	header, err := direct.requestHeaders(ctx, http.MethodGet, "/me", nil, new(Identity))
	if err != nil {
		return netip.Addr{}, err
	}
	address, err := netip.ParseAddr(header.Get("Sliplane-Client-IP"))
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, errors.New("API did not report a valid public IP address")
	}
	return address.Unmap(), nil
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
