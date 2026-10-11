package api

import (
	"context"
	"net/url"
	"strconv"
)

func (c *Client) ApproveRouteLifecycle(ctx context.Context, slug string, r ApproveRouteLifecycleRequest) (RouteLifecycleApproval, error) {
	var out RouteLifecycleApproval
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/route-lifecycle/approvals", r, &out)
	return out, err
}
func (c *Client) GetRouteLifecycleApproval(ctx context.Context, slug, id string) (RouteLifecycleApproval, error) {
	var out RouteLifecycleApproval
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-lifecycle/approvals/"+url.PathEscape(id), nil, &out)
	return out, err
}

func (c *Client) ListRouteLifecycleHistory(ctx context.Context, slug string, limit int, before string) (RouteLifecycleHistoryPage, error) {
	var out RouteLifecycleHistoryPage
	query := url.Values{}
	query.Set("limit", strconv.Itoa(limit))
	if before != "" {
		query.Set("before", before)
	}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-lifecycle/history?"+query.Encode(), nil, &out)
	return out, err
}
