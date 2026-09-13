package api

import (
	"context"
	"net/http"
	"net/url"
)

// GetStatus reads the unauthenticated public status overview.
func (c *Client) GetStatus(ctx context.Context) (PublicStatusOverview, error) {
	var out PublicStatusOverview
	err := c.do(ctx, http.MethodGet, "/v1/status", nil, &out)
	return out, err
}

// GetStatusIncidentsPublic_id reads an incident permalink. The method name
// intentionally follows the SDK coverage convention for {public_id} routes.
func (c *Client) GetStatusIncidentsPublic_id(ctx context.Context, publicID string) (PublicStatusEvent, error) {
	var out PublicStatusEvent
	err := c.do(ctx, http.MethodGet, "/v1/status/incidents/"+url.PathEscape(publicID), nil, &out)
	return out, err
}

// GetAdminStatusIncidents lists operator-managed status events.
func (c *Client) GetAdminStatusIncidents(ctx context.Context, kind string, active bool) ([]PublicStatusEvent, error) {
	query := url.Values{}
	if kind != "" {
		query.Set("kind", kind)
	}
	if active {
		query.Set("active", "true")
	}
	path := cookieOnlyAdminStatusPath
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []PublicStatusEvent
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// PostAdminStatusIncidents publishes an incident or maintenance event.
func (c *Client) PostAdminStatusIncidents(ctx context.Context, request AdminStatusEventCreateRequest, idempotencyKey string) (PublicStatusEvent, error) {
	var out PublicStatusEvent
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, cookieOnlyAdminStatusPath, request, &out, idempotencyKey)
	return out, err
}

// PostAdminStatusIncidentsPublic_idUpdates appends a lifecycle update.
func (c *Client) PostAdminStatusIncidentsPublic_idUpdates(ctx context.Context, publicID string, request AdminStatusEventUpdateRequest, idempotencyKey string) (PublicStatusEvent, error) {
	var out PublicStatusEvent
	path := cookieOnlyAdminStatusPath + "/" + url.PathEscape(publicID) + "/updates"
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, path, request, &out, idempotencyKey)
	return out, err
}
