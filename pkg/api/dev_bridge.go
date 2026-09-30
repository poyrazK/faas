package api

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/devbridge"
)

type CreateDevBridgeRequest struct {
	App          string   `json:"app"`
	Environment  string   `json:"environment"`
	DeveloperID  string   `json:"developer_id"`
	Dependencies []string `json:"dependencies,omitempty"`
	Entrypoint   string   `json:"entrypoint,omitempty"`
}

// Credentials are returned once, by creation only. Inspection returns Session.
type CreateDevBridgeResponse struct {
	Session        devbridge.Session     `json:"session"`
	Credentials    devbridge.Credentials `json:"credentials"`
	EnvironmentURL string                `json:"environment_url"`
	Dependencies   []DevBridgeDependency `json:"dependencies,omitempty"`
}

type DevBridgeDependency struct {
	AppID          string `json:"app_id"`
	Name           string `json:"name"`
	EnvironmentURL string `json:"environment_url"`
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

type ReplayDevBridgeWebhookRequest struct {
	InvocationID   string `json:"invocation_id"`
	RequestToken   string `json:"request_token"`
	IdempotencyKey string `json:"idempotency_key"`
}

func (c *Client) ReplayDevBridgeWebhook(ctx context.Context, id string, request ReplayDevBridgeWebhookRequest) (devbridge.WebhookReplay, error) {
	var out devbridge.WebhookReplay
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = DevBridgeWebhookReplayTimeout + 5*time.Second
	err := c.doWithClientAndIdempotencyKey(ctx, &client, "POST", "/v1/dev/bridges/"+url.PathEscape(id)+"/webhook-replays", request, &out, "")
	return out, err
}

func (c *Client) GetDevBridgeWebhookReplay(ctx context.Context, session, replay string) (devbridge.WebhookReplay, error) {
	var out devbridge.WebhookReplay
	err := c.do(ctx, "GET", "/v1/dev/bridges/"+url.PathEscape(session)+"/webhook-replays/"+url.PathEscape(replay), nil, &out)
	return out, err
}
