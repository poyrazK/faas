package api

import (
	"context"
	"net/url"
)

func routePrioritiesPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/route-priorities"
}

// GetRoutePriorities reads an app's effective route priorities (ADR-947).
func (c *Client) GetRoutePriorities(ctx context.Context, slug string) (RoutePrioritiesResponse, error) {
	var out RoutePrioritiesResponse
	return out, c.do(ctx, "GET", routePrioritiesPath(slug), nil, &out)
}

// SetRoutePriorities replaces an app's saved route priorities.
func (c *Client) SetRoutePriorities(ctx context.Context, slug string, rules []RoutePriorityRule) (RoutePrioritiesResponse, error) {
	if rules == nil {
		rules = []RoutePriorityRule{}
	}
	var out RoutePrioritiesResponse
	return out, c.do(ctx, "PUT", routePrioritiesPath(slug), SetRoutePrioritiesRequest{Routes: rules}, &out)
}

// ResetRoutePriorities deletes saved priorities, restoring the route-health
// default.
func (c *Client) ResetRoutePriorities(ctx context.Context, slug string) (RoutePrioritiesResponse, error) {
	var out RoutePrioritiesResponse
	return out, c.do(ctx, "DELETE", routePrioritiesPath(slug), nil, &out)
}
