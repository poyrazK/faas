package api

import (
	"context"
	"encoding/json"
	"net/url"
	"time"
)

type ManagedRealtimeSignalRequest struct {
	Data  json.RawMessage `json:"data"`
	Name  string          `json:"name,omitempty"`
	TTLMS *int            `json:"ttl_ms,omitempty"`
}
type ManagedRealtimeSignalResponse struct {
	Accepted  bool       `json:"accepted"`
	MemberID  string     `json:"member_id"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

func (c *Client) PublishManagedRealtimeSignal(ctx context.Context, slug, ep, ch string, req ManagedRealtimeSignalRequest) (ManagedRealtimeSignalResponse, error) {
	var out ManagedRealtimeSignalResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/realtime/endpoints/" + url.PathEscape(ep) + "/channels/" + url.PathEscape(ch) + "/signals"
	err := c.do(ctx, "POST", path, req, &out)
	return out, err
}

func (c *Client) PublishManagedRealtimeScopedSignal(ctx context.Context, slug, ep, parent, scope string, req ManagedRealtimeSignalRequest) (ManagedRealtimeSignalResponse, error) {
	if _, err := RealtimeActivityScopeChannel(parent, scope); err != nil {
		return ManagedRealtimeSignalResponse{}, err
	}
	var out ManagedRealtimeSignalResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/realtime/endpoints/" + url.PathEscape(ep) + "/channels/" + url.PathEscape(parent) + "/activity-scopes/" + url.PathEscape(scope) + "/signals"
	err := c.do(ctx, "POST", path, req, &out)
	return out, err
}
