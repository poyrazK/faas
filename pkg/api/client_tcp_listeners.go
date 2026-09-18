package api

import (
	"context"
	"net/http"
	"net/url"
)

// ListAppTCPListeners returns the durable raw-TCP listeners owned by an app.
func (c *Client) ListAppTCPListeners(ctx context.Context, slug string) ([]TCPListenerResponse, error) {
	var out []TCPListenerResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/tcp-listeners", nil, &out)
	return out, err
}

// CreateAppTCPListener creates one enabled raw-TCP listener.
func (c *Client) CreateAppTCPListener(ctx context.Context, slug string, req CreateTCPListenerRequest) (TCPListenerResponse, error) {
	var out TCPListenerResponse
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/tcp-listeners", req, &out)
	return out, err
}

// UpdateAppTCPListener enables or disables a raw-TCP listener.
func (c *Client) UpdateAppTCPListener(ctx context.Context, slug, name string, req UpdateTCPListenerRequest) (TCPListenerResponse, error) {
	var out TCPListenerResponse
	err := c.do(ctx, http.MethodPatch, "/v1/apps/"+url.PathEscape(slug)+"/tcp-listeners/"+url.PathEscape(name), req, &out)
	return out, err
}

// DeleteAppTCPListener removes a raw-TCP listener.
func (c *Client) DeleteAppTCPListener(ctx context.Context, slug, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/tcp-listeners/"+url.PathEscape(name), nil, nil)
}
