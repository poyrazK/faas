package api

import (
	"context"
	"net/url"
)

func (c *Client) GetRouteHealthGate(ctx context.Context, slug string) (RouteHealthGate, error) {
	var out RouteHealthGate
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-health/gate", nil, &out)
	return out, err
}
func (c *Client) SetRouteHealthGate(ctx context.Context, slug string, request SetRouteHealthGateRequest) (RouteHealthGate, error) {
	var out RouteHealthGate
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/route-health/gate", request, &out)
	return out, err
}
func (c *Client) GetRouteHealthReport(ctx context.Context, slug, deploymentID string) (RouteHealthReport, error) {
	var out RouteHealthReport
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-health/deployments/"+url.PathEscape(deploymentID), nil, &out)
	return out, err
}
