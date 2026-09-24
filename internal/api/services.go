// Service creation sends image configuration and new volume mounts in one API request.
package api

import (
	"context"
	"net/http"
	"net/url"
)

type CreateServiceInput struct {
	Name        string                `json:"name"`
	ServerID    string                `json:"serverId"`
	Deployment  ImageDeployment       `json:"deployment"`
	Network     ServiceNetwork        `json:"network"`
	Env         []EnvironmentVariable `json:"env,omitempty"`
	Volumes     []NewServiceVolume    `json:"volumes,omitempty"`
	Healthcheck string                `json:"healthcheck,omitempty"`
	Cmd         string                `json:"cmd,omitempty"`
}

type ImageDeployment struct {
	URL string `json:"url"`
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

type NewServiceVolume struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

// CreateService provisions a new service; the returned state does not imply readiness.
func (c *Client) CreateService(ctx context.Context, projectID string, input CreateServiceInput) (Service, error) {
	var service Service
	err := c.request(ctx, http.MethodPost, "/projects/"+url.PathEscape(projectID)+"/services", input, &service)
	return service, err
}
