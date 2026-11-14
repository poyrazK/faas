package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ToolOAuthAuthorizationRequest is an owner-only, transient PKCE command. Codes
// and state never enter invocation journals or agent projections.
type ToolOAuthAuthorizationRequest struct {
	Action      string `json:"action"`
	RedirectURI string `json:"redirect_uri,omitempty"`
	State       string `json:"state,omitempty"`
	Code        string `json:"code,omitempty"`
	Denied      bool   `json:"denied,omitempty"`
	Issuer      string `json:"issuer,omitempty"`
}

type ToolOAuthAuthorizationResponse struct {
	ConnectionID     string     `json:"connection_id"`
	State            string     `json:"state"`
	AuthorizationURL string     `json:"authorization_url,omitempty"`
	CallbackState    string     `json:"callback_state,omitempty"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
}

func (c *Client) AuthorizeToolOAuthConnection(ctx context.Context, id string, in ToolOAuthAuthorizationRequest) (ToolOAuthAuthorizationResponse, error) {
	var out ToolOAuthAuthorizationResponse
	err := c.doWithoutIdempotencyKey(ctx, http.MethodPost, "/v1/tool-oauth-connections/"+url.PathEscape(id)+"/authorization", in, &out)
	return out, err
}

func (c *Client) GetToolOAuthAuthorization(ctx context.Context, id string) (ToolOAuthAuthorizationResponse, error) {
	var out ToolOAuthAuthorizationResponse
	err := c.do(ctx, http.MethodGet, "/v1/tool-oauth-connections/"+url.PathEscape(id)+"/authorization", nil, &out)
	return out, err
}
