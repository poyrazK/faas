package api

import (
	"context"
	"net/url"
)

// GetRouteInsights reads an app's route insights (ADR-1198).
func (c *Client) GetRouteInsights(ctx context.Context, slug string) (RouteInsightsResponse, error) {
	var out RouteInsightsResponse
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/routes/insights", nil, &out)
}
