package api

import (
	"context"
	"net/url"
	"strconv"
)

func (c *Client) ListRouteHealthHistory(ctx context.Context, slug, deploymentID string, limit int, before string) (RouteHealthHistoryPage, error) {
	var out RouteHealthHistoryPage
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if before != "" {
		query.Set("before", before)
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-health/deployments/"+url.PathEscape(deploymentID)+"/history?"+query.Encode(), nil, &out)
	return out, err
}

func (c *Client) GetRouteHealthHistoryEntry(ctx context.Context, slug, deploymentID, decisionID string) (RouteHealthHistoryEntry, error) {
	var out RouteHealthHistoryEntry
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-health/deployments/"+url.PathEscape(deploymentID)+"/history/"+url.PathEscape(decisionID), nil, &out)
	return out, err
}
