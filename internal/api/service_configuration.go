package api

import (
	"context"
	"net/http"
	"net/url"
)

type CustomDomain struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Status string `json:"status"`
}

type ServiceNetworkDetails struct {
	Public         bool           `json:"public"`
	Protocol       string         `json:"protocol,omitempty"`
	ManagedDomain  string         `json:"managedDomain,omitempty"`
	InternalDomain string         `json:"internalDomain,omitempty"`
	InternalPort   int            `json:"internalPort,omitempty"`
	CustomDomains  []CustomDomain `json:"customDomains,omitempty"`
}

func (c *Client) ServiceEnvironment(ctx context.Context, projectID, serviceID string) ([]EnvironmentVariable, error) {
	return list[EnvironmentVariable](ctx, c, servicePath(projectID, serviceID)+"/env")
}

func (c *Client) SetServiceEnvironment(ctx context.Context, projectID, serviceID string, entry EnvironmentVariable) error {
	input := struct {
		Value  string `json:"value"`
		Secret bool   `json:"secret"`
	}{entry.Value, entry.Secret}
	return c.request(ctx, http.MethodPut, servicePath(projectID, serviceID)+"/env/"+url.PathEscape(entry.Key), input, nil)
}

func (c *Client) DeleteServiceEnvironment(ctx context.Context, projectID, serviceID, key string) error {
	return c.request(ctx, http.MethodDelete, servicePath(projectID, serviceID)+"/env/"+url.PathEscape(key), nil, nil)
}

func (c *Client) AddServiceDomain(ctx context.Context, projectID, serviceID, domain string) (CustomDomain, error) {
	var result CustomDomain
	err := c.request(ctx, http.MethodPost, servicePath(projectID, serviceID)+"/domains", map[string]string{"domain": domain}, &result)
	return result, err
}

func (c *Client) DeleteServiceDomain(ctx context.Context, projectID, serviceID, domainID string) error {
	return c.request(ctx, http.MethodDelete, servicePath(projectID, serviceID)+"/domains/"+url.PathEscape(domainID), nil, nil)
}
