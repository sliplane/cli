package api

import (
	"context"
	"net/http"
	"net/url"
)

type Volume struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath,omitempty"`
}

func (c *Client) ServerVolumes(ctx context.Context, serverID string) ([]Volume, error) {
	return list[Volume](ctx, c, "/servers/"+url.PathEscape(serverID)+"/volumes")
}

func (c *Client) CreateVolume(ctx context.Context, serverID, name string) (Volume, error) {
	var volume Volume
	err := c.request(ctx, http.MethodPost, "/servers/"+url.PathEscape(serverID)+"/volumes", map[string]string{"name": name}, &volume)
	return volume, err
}

func (c *Client) RenameVolume(ctx context.Context, serverID, volumeID, name string) (Volume, error) {
	var volume Volume
	err := c.request(ctx, http.MethodPatch, "/servers/"+url.PathEscape(serverID)+"/volumes/"+url.PathEscape(volumeID), map[string]string{"name": name}, &volume)
	return volume, err
}

func (c *Client) AttachVolume(ctx context.Context, projectID, serviceID string, input ServiceVolume) (Volume, error) {
	var volume Volume
	err := c.request(ctx, http.MethodPost, servicePath(projectID, serviceID)+"/volumes", input, &volume)
	return volume, err
}

func (c *Client) DetachVolume(ctx context.Context, projectID, serviceID, volumeID string) error {
	return c.request(ctx, http.MethodDelete, servicePath(projectID, serviceID)+"/volumes/"+url.PathEscape(volumeID), nil, nil)
}

func (c *Client) UpdateVolumeMount(ctx context.Context, projectID, serviceID, volumeID, mountPath string) (Volume, error) {
	var volume Volume
	err := c.request(ctx, http.MethodPatch, servicePath(projectID, serviceID)+"/volumes/"+url.PathEscape(volumeID), map[string]string{"mountPath": mountPath}, &volume)
	return volume, err
}
