package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ToolOAuthConnectionIntent is owner-approved execution authority. References
// name existing sealed app secrets; credential values never enter these APIs.
type ToolOAuthConnectionIntent struct {
	Guided                bool     `json:"guided,omitempty"`
	AuthorizationEndpoint string   `json:"authorization_endpoint,omitempty"`
	App                   string   `json:"app"`
	Scope                 string   `json:"scope"`
	Issuer                string   `json:"issuer"`
	TokenEndpoint         string   `json:"token_endpoint"`
	ClientID              string   `json:"client_id"`
	Resource              string   `json:"resource"`
	Scopes                []string `json:"scopes"`
	RefreshTokenSecretRef string   `json:"refresh_token_secret_ref,omitempty"`
	ClientSecretRef       string   `json:"client_secret_ref,omitempty"`
	TokenAuthMethod       string   `json:"token_auth_method"`
}

type ToolOAuthConnectionRecord struct {
	ID                 string                    `json:"id"`
	AccountID          string                    `json:"account_id"`
	AppID              string                    `json:"app_id"`
	ProjectID          string                    `json:"project_id,omitempty"`
	BindingSHA256      string                    `json:"binding_sha256"`
	SourceSHA256       string                    `json:"source_sha256"`
	ClientSourceSHA256 string                    `json:"client_source_sha256,omitempty"`
	Intent             ToolOAuthConnectionIntent `json:"intent"`
	CreatedAt          time.Time                 `json:"created_at"`
	RevokedAt          *time.Time                `json:"revoked_at,omitempty"`
	State              string                    `json:"state"`
	ExpiresAt          *time.Time                `json:"expires_at,omitempty"`
	Generation         int64                     `json:"generation"`
	RecoveryAction     string                    `json:"recovery_action,omitempty"`
}

type ToolOAuthConnectionPage struct {
	Connections []ToolOAuthConnectionRecord `json:"connections"`
	NextCursor  string                      `json:"next_cursor,omitempty"`
}

// ToolOAuthConnectionStatus is the only agent projection. No provider settings,
// secret names, token values or ciphertext are included.
type ToolOAuthConnectionStatus struct {
	GrantID        string     `json:"grant_id"`
	App            string     `json:"app"`
	AppID          string     `json:"app_id"`
	Version        string     `json:"version"`
	Name           string     `json:"name"`
	State          string     `json:"state"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	RecoveryAction string     `json:"recovery_action,omitempty"`
}

func (c *Client) PutToolOAuthConnection(ctx context.Context, id string, in ToolOAuthConnectionIntent) (ToolOAuthConnectionRecord, error) {
	var out ToolOAuthConnectionRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPut, "/v1/tool-oauth-connections/"+url.PathEscape(id), in, &out)
	return out, err
}
func (c *Client) GetToolOAuthConnection(ctx context.Context, id string) (ToolOAuthConnectionRecord, error) {
	var out ToolOAuthConnectionRecord
	err := c.do(ctx, http.MethodGet, "/v1/tool-oauth-connections/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) RevokeToolOAuthConnection(ctx context.Context, id string) (ToolOAuthConnectionRecord, error) {
	var out ToolOAuthConnectionRecord
	err := c.doWithoutIdempotencyKey(ctx, http.MethodDelete, "/v1/tool-oauth-connections/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ListToolOAuthConnections(ctx context.Context, cursor string, limit int) (ToolOAuthConnectionPage, error) {
	var out ToolOAuthConnectionPage
	q := url.Values{"cursor": {cursor}, "limit": {strconv.Itoa(limit)}}
	err := c.do(ctx, http.MethodGet, "/v1/tool-oauth-connections?"+q.Encode(), nil, &out)
	return out, err
}
func (c *Client) GetDelegatedToolConnection(ctx context.Context, grant string) (ToolOAuthConnectionStatus, error) {
	var out ToolOAuthConnectionStatus
	err := c.do(ctx, http.MethodGet, "/v1/delegated-tool-connections/"+url.PathEscape(grant), nil, &out)
	return out, err
}
