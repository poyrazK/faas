package api

import (
	"context"
	"net/url"
	"strconv"
)

// RouteAdviceOptions are the optional query parameters of GetRouteAdvice.
type RouteAdviceOptions struct {
	Since              string
	Until              string
	CacheMaxAgeSeconds int
}

// GetRouteAdvice reads route advisor suggestions for an app.
func (c *Client) GetRouteAdvice(ctx context.Context, slug string, opts RouteAdviceOptions) (RouteAdviceResponse, error) {
	var out RouteAdviceResponse
	q := url.Values{}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	if opts.CacheMaxAgeSeconds > 0 {
		q.Set("cache_max_age", strconv.Itoa(opts.CacheMaxAgeSeconds))
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/routes/advice"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return out, c.do(ctx, "GET", path, nil, &out)
}
