package api

import (
	"context"
	"net/url"
)

func (c *Client) GetRouteRemovalPolicy(ctx context.Context, slug string) (RouteRemovalPolicy, error) {
	var out RouteRemovalPolicy
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-removal/policy", nil, &out)
	return out, err
}
func (c *Client) SetRouteRemovalPolicy(ctx context.Context, slug string, r SetRouteRemovalPolicyRequest) (RouteRemovalPolicy, error) {
	var out RouteRemovalPolicy
	err := c.do(ctx, "PUT", "/v1/apps/"+url.PathEscape(slug)+"/route-removal/policy", r, &out)
	return out, err
}
func (c *Client) ApproveRouteRemoval(ctx context.Context, slug string, r ApproveRouteRemovalRequest) (RouteRemovalApproval, error) {
	var out RouteRemovalApproval
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/route-removal/approvals", r, &out)
	return out, err
}
func (c *Client) CheckRouteRemoval(ctx context.Context, slug, id string) (RouteRemovalCheck, error) {
	var out RouteRemovalCheck
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/route-removal/check?deployment_id="+url.QueryEscape(id), nil, &out)
	return out, err
}
