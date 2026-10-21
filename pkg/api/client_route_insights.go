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

// FixRouteInsight applies an insight's fix; fixID must be the fix.id the
// insight listed (ADR-20261020161310).
func (c *Client) FixRouteInsight(ctx context.Context, slug, insightID, fixID string) (RouteInsightFixResponse, error) {
	var out RouteInsightFixResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/routes/insights/" + url.PathEscape(insightID) + "/fix"
	return out, c.do(ctx, "POST", path, RouteInsightFixRequest{FixID: fixID}, &out)
}
