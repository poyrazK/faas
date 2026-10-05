package api

import (
	"context"
	"net/url"
)

// RouteCustomerUsageOptions selects an immutable deployment and optional
// half-open telemetry window. Since accepts a duration or RFC3339 timestamp.
type RouteCustomerUsageOptions struct {
	DeploymentID string
	Since        string
	Until        string
}

// GetAppRouteCustomerUsage reads bounded customer exposure for a deployment.
// Returned identifiers belong to the account; names, keys and payloads are absent.
func (c *Client) GetAppRouteCustomerUsage(ctx context.Context, slug string, opts RouteCustomerUsageOptions) (RouteCustomerUsageResponse, error) {
	var out RouteCustomerUsageResponse
	q := url.Values{"deployment_id": {opts.DeploymentID}}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Until != "" {
		q.Set("until", opts.Until)
	}
	return out, c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/analytics/route-customers?"+q.Encode(), nil, &out)
}
