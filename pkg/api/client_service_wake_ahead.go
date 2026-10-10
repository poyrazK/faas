package api

import (
	"context"
	"net/url"
)

// GetServiceWakeAhead reads an app's wake-ahead opt-in (ADR-946).
func (c *Client) GetServiceWakeAhead(ctx context.Context, slug string) (ServiceWakeAheadResponse, error) {
	var out ServiceWakeAheadResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/service-wake-ahead", nil, &out)
}

// SetServiceWakeAhead turns an app's wake-ahead on or off (ADR-946).
func (c *Client) SetServiceWakeAhead(ctx context.Context, slug string, enabled bool) (ServiceWakeAheadResponse, error) {
	var out ServiceWakeAheadResponse
	return out, c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/service-wake-ahead", SetServiceWakeAheadRequest{Enabled: &enabled}, &out)
}
