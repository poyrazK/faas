package api

import (
	"context"
	"net/url"
)

// RouteAwakeOptions are the optional query parameters of GetRouteAwake.
type RouteAwakeOptions struct {
	Since string
	Until string
}

// GetRouteAwake reads which routes kept an app awake (ADR-1187).
func (c *Client) GetRouteAwake(ctx context.Context, slug string, opts RouteAwakeOptions) (RouteAwakeResponse, error) {
	var out RouteAwakeResponse
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/routes/awake"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}
