package api

import (
	"context"
	"net/http"
	"net/url"
)

// ListAppUDPListeners returns the durable raw-UDP listeners owned by an app.
func (c *Client) ListAppUDPListeners(ctx context.Context, slug string) ([]UDPListenerResponse, error) {
	var out []UDPListenerResponse
	err := c.do(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/udp-listeners", nil, &out)
	return out, err
}

// CreateAppUDPListener creates one disabled raw-UDP listener.
func (c *Client) CreateAppUDPListener(ctx context.Context, slug string, req CreateUDPListenerRequest) (UDPListenerResponse, error) {
	var out UDPListenerResponse
	err := c.do(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/udp-listeners", req, &out)
	return out, err
}

// UpdateAppUDPListener enables or disables a raw-UDP listener.
func (c *Client) UpdateAppUDPListener(ctx context.Context, slug, name string, req UpdateUDPListenerRequest) (UDPListenerResponse, error) {
	var out UDPListenerResponse
	err := c.do(ctx, http.MethodPatch, "/v1/apps/"+url.PathEscape(slug)+"/udp-listeners/"+url.PathEscape(name), req, &out)
	return out, err
}

// DeleteAppUDPListener removes a raw-UDP listener.
func (c *Client) DeleteAppUDPListener(ctx context.Context, slug, name string) error {
	return c.do(ctx, http.MethodDelete, "/v1/apps/"+url.PathEscape(slug)+"/udp-listeners/"+url.PathEscape(name), nil, nil)
}
