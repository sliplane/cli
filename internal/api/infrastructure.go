package api

import (
	"context"
	"net/http"
	"net/url"
)

type CreateServerInput struct {
	Name         string `json:"name"`
	InstanceType string `json:"instanceType"`
	Location     string `json:"location"`
	BillingCycle string `json:"billingCycle,omitempty"`
	DiskSizeGB   int    `json:"diskSizeGb,omitempty"`
}

func (c *Client) CreateServer(ctx context.Context, input CreateServerInput) (Server, error) {
	var server Server
	err := c.request(ctx, http.MethodPost, "/servers", input, &server)
	return server, err
}

func (c *Client) ScaleServer(ctx context.Context, id, instanceType, billingCycle string) error {
	return c.request(ctx, http.MethodPost, "/servers/"+url.PathEscape(id), struct {
		InstanceType string `json:"instanceType"`
		BillingCycle string `json:"billingCycle,omitempty"`
	}{instanceType, billingCycle}, nil)
}

func (c *Client) ScaleServerDisk(ctx context.Context, id string, size int) error {
	return c.request(ctx, http.MethodPost, "/servers/"+url.PathEscape(id)+"/disk", map[string]int{"diskSizeGb": size}, nil)
}

func (c *Client) DeleteServer(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/servers/"+url.PathEscape(id), nil, nil)
}

func (c *Client) RenameProject(ctx context.Context, id, name string) (Project, error) {
	var project Project
	err := c.request(ctx, http.MethodPatch, "/projects/"+url.PathEscape(id), map[string]string{"name": name}, &project)
	return project, err
}

func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.request(ctx, http.MethodDelete, "/projects/"+url.PathEscape(id), nil, nil)
}
