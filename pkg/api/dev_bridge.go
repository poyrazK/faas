package api

import (
	"context"
	"net/url"

	"github.com/onebox-faas/faas/pkg/devbridge"
)

type CreateDevBridgeRequest struct {
	App          string   `json:"app"`
	Environment  string   `json:"environment"`
	DeveloperID  string   `json:"developer_id"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// Credentials are returned once, by creation only. Inspection returns Session.
type CreateDevBridgeResponse struct {
	Session        devbridge.Session     `json:"session"`
	Credentials    devbridge.Credentials `json:"credentials"`
	EnvironmentURL string                `json:"environment_url"`
	Dependencies   []DevBridgeDependency `json:"dependencies,omitempty"`
}

type DevBridgeDependency struct {
	AppID string `json:"app_id"`
	Name  string `json:"name"`
}

func (c *Client) CreateDevBridge(ctx context.Context, request CreateDevBridgeRequest) (CreateDevBridgeResponse, error) {
	var out CreateDevBridgeResponse
	err := c.do(ctx, "POST", "/v1/dev/bridges", request, &out)
	return out, err
}

func (c *Client) GetDevBridge(ctx context.Context, id string) (devbridge.Session, error) {
	var out devbridge.Session
	err := c.do(ctx, "GET", "/v1/dev/bridges/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) RevokeDevBridge(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/dev/bridges/"+url.PathEscape(id), nil, nil)
}
