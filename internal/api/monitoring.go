package api

import (
	"context"
	"encoding/json"
	"net/url"
)

type Metric struct {
	CreatedAt   string  `json:"createdAt"`
	CPUUsage    float64 `json:"cpuUsage"`
	UsedMemory  float64 `json:"usedMemory"`
	FreeMemory  float64 `json:"freeMemory"`
	TotalMemory float64 `json:"totalMemory"`
}

type ServiceEvent struct {
	ID        string          `json:"id"`
	CreatedAt string          `json:"createdAt"`
	Type      string          `json:"type"`
	Message   string          `json:"message"`
	Trigger   string          `json:"trigger"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func (c *Client) ServiceMetrics(ctx context.Context, projectID, serviceID string, query url.Values) ([]Metric, error) {
	return list[Metric](ctx, c, servicePath(projectID, serviceID)+"/metrics?"+query.Encode())
}

func (c *Client) ServerMetrics(ctx context.Context, serverID string, query url.Values) ([]Metric, error) {
	return list[Metric](ctx, c, "/servers/"+url.PathEscape(serverID)+"/metrics?"+query.Encode())
}

func (c *Client) ServiceEvents(ctx context.Context, projectID, serviceID string) ([]ServiceEvent, error) {
	return list[ServiceEvent](ctx, c, servicePath(projectID, serviceID)+"/events")
}
