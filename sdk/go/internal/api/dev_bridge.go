package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

const devBridgeWebhookReplayTimeout = 30 * time.Second

type DevBridgeScope struct {
	AccountID        string   `json:"account_id"`
	DeveloperID      string   `json:"developer_id"`
	EnvironmentID    string   `json:"environment_id"`
	ProjectID        string   `json:"project_id"`
	TargetAppID      string   `json:"target_app_id"`
	DependencyAppIDs []string `json:"dependency_app_ids,omitempty"`
}
type DevBridgeSession struct {
	ID        string         `json:"id"`
	Scope     DevBridgeScope `json:"scope"`
	ExpiresAt time.Time      `json:"expires_at"`
	RevokedAt *time.Time     `json:"revoked_at,omitempty"`
}
type DevBridgeCredentials struct {
	AttachmentToken string `json:"attachment_token"`
	RequestToken    string `json:"request_token"`
}
type DevBridgeWebhookReplay struct {
	ID           string     `json:"id"`
	SessionID    string     `json:"session_id"`
	InvocationID string     `json:"invocation_id"`
	State        string     `json:"state"`
	HTTPStatus   int        `json:"http_status"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

type CreateDevBridgeRequest struct {
	App          string   `json:"app"`
	Environment  string   `json:"environment"`
	DeveloperID  string   `json:"developer_id"`
	Dependencies []string `json:"dependencies,omitempty"`
	Entrypoint   string   `json:"entrypoint,omitempty"`
}

// Credentials are returned once, by creation only. Inspection returns Session.
type CreateDevBridgeResponse struct {
	Session        DevBridgeSession      `json:"session"`
	Credentials    DevBridgeCredentials  `json:"credentials"`
	EnvironmentURL string                `json:"environment_url"`
	Dependencies   []DevBridgeDependency `json:"dependencies,omitempty"`
}

type DevBridgeDependency struct {
	AppID          string `json:"app_id"`
	Name           string `json:"name"`
	EnvironmentURL string `json:"environment_url"`
}

type DevBridgeSessionSummary struct {
	Session         DevBridgeSession `json:"session"`
	ConnectionState string           `json:"connection_state"`
}
type ListDevBridgesResponse struct {
	Sessions []DevBridgeSessionSummary `json:"sessions"`
}
type DevBridgeRequestRecord struct {
	ID            uint64    `json:"id"`
	StartedAt     time.Time `json:"started_at"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	Status        int       `json:"status"`
	DurationMS    int64     `json:"duration_ms"`
	ResponseBytes int64     `json:"response_bytes"`
	Complete      bool      `json:"complete"`
	Error         string    `json:"error,omitempty"`
}
type DevBridgeActivity struct {
	ConnectionState string                   `json:"connection_state"`
	ConnectedAt     *time.Time               `json:"connected_at,omitempty"`
	DisconnectedAt  *time.Time               `json:"disconnected_at,omitempty"`
	Requests        []DevBridgeRequestRecord `json:"requests"`
}

func (c *Client) ListDevBridges(ctx context.Context) (ListDevBridgesResponse, error) {
	var out ListDevBridgesResponse
	err := c.do(ctx, "GET", "/v1/dev/bridges", nil, &out)
	return out, err
}

func (c *Client) GetDevBridgeActivity(ctx context.Context, id string) (DevBridgeActivity, error) {
	var out DevBridgeActivity
	err := c.do(ctx, "GET", "/v1/dev/bridges/"+url.PathEscape(id)+"/activity", nil, &out)
	return out, err
}

func (c *Client) CreateDevBridge(ctx context.Context, request CreateDevBridgeRequest) (CreateDevBridgeResponse, error) {
	var out CreateDevBridgeResponse
	err := c.do(ctx, "POST", "/v1/dev/bridges", request, &out)
	return out, err
}

func (c *Client) GetDevBridge(ctx context.Context, id string) (DevBridgeSession, error) {
	var out DevBridgeSession
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

func (c *Client) ReplayDevBridgeWebhook(ctx context.Context, id string, request ReplayDevBridgeWebhookRequest) (DevBridgeWebhookReplay, error) {
	var out DevBridgeWebhookReplay
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = devBridgeWebhookReplayTimeout + 5*time.Second
	copy := NewClient(c.baseURL, c.Token())
	copy.http = &client
	err := copy.do(ctx, "POST", "/v1/dev/bridges/"+url.PathEscape(id)+"/webhook-replays", request, &out)
	return out, err
}

func (c *Client) GetDevBridgeWebhookReplay(ctx context.Context, session, replay string) (DevBridgeWebhookReplay, error) {
	var out DevBridgeWebhookReplay
	err := c.do(ctx, "GET", "/v1/dev/bridges/"+url.PathEscape(session)+"/webhook-replays/"+url.PathEscape(replay), nil, &out)
	return out, err
}
