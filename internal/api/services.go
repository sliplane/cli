package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

type CreateServiceInput struct {
	Name        string                `json:"name"`
	ServerID    string                `json:"serverId"`
	Deployment  ServiceDeployment     `json:"deployment"`
	Network     ServiceNetwork        `json:"network"`
	Env         []EnvironmentVariable `json:"env,omitempty"`
	Volumes     []ServiceVolume       `json:"volumes,omitempty"`
	Healthcheck string                `json:"healthcheck,omitempty"`
	Cmd         string                `json:"cmd,omitempty"`
}

// ServiceDeployment carries the selected source's fields; callers reject image/repository flag combinations.
type ServiceDeployment struct {
	URL                      string   `json:"url"`
	RegistryAuthenticationID string   `json:"registryAuthenticationId,omitempty"`
	Branch                   string   `json:"branch,omitempty"`
	AutoDeploy               *bool    `json:"autoDeploy,omitempty"`
	DockerfilePath           *string  `json:"dockerfilePath,omitempty"`
	DockerContext            string   `json:"dockerContext,omitempty"`
	IncludePaths             []string `json:"includePaths,omitempty"`
	IgnorePaths              []string `json:"ignorePaths,omitempty"`
}

type ServiceNetwork struct {
	Public   bool   `json:"public"`
	Protocol string `json:"protocol,omitempty"`
}

type EnvironmentVariable struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// ServiceVolume selects either a new volume name or an existing volume ID, with its container mount path.
type ServiceVolume struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	MountPath string `json:"mountPath"`
}

func (c *Client) CreateService(ctx context.Context, projectID string, input CreateServiceInput) (Service, error) {
	var service Service
	err := c.request(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/services", input, &service)
	return service, err
}

// UpdateServiceInput changes only the supplied service settings.
type UpdateServiceInput struct {
	Deployment  map[string]json.RawMessage `json:"deployment,omitempty"`
	Name        *string                    `json:"name,omitempty"`
	Healthcheck *string                    `json:"healthcheck,omitempty"`
	Cmd         *string                    `json:"cmd,omitempty"`
}

func (c *Client) UpdateService(ctx context.Context, projectID, serviceID string, input UpdateServiceInput) (Service, error) {
	var service Service
	err := c.request(ctx, http.MethodPatch, servicePath(projectID, serviceID), input, &service)
	return service, err
}

func (c *Client) DeleteService(ctx context.Context, projectID, serviceID string) error {
	return c.request(ctx, http.MethodDelete, servicePath(projectID, serviceID), nil, nil)
}

func (c *Client) RedeployService(ctx context.Context, projectID, serviceID, tag string) error {
	input := struct {
		Tag string `json:"tag,omitempty"`
	}{Tag: tag}
	return c.request(ctx, http.MethodPost, servicePath(projectID, serviceID)+"/deploy", input, nil)
}

func (c *Client) ServiceAction(ctx context.Context, projectID, serviceID, action string) error {
	return c.request(ctx, http.MethodPost, servicePath(projectID, serviceID)+"/"+action, nil, nil)
}
