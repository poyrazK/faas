package api

import (
	"context"
	"net/url"
)

// GetAppDebugRegressionReview reads exactly one observation without the active
// inbox visibility filter. A missing endpoint is not permission to write unguarded.
func (c *Client) GetAppDebugRegressionReview(ctx context.Context, slug, deploymentID, route string) (DebugRegressionReview, error) {
	var out DebugRegressionReview
	q := url.Values{"deployment_id": {deploymentID}, "route": {route}}
	err := c.do(ctx, "GET", "/v1/apps/"+url.PathEscape(slug)+"/debug/regressions/review?"+q.Encode(), nil, &out)
	return out, err
}

// ApplyAppDebugRegressionAction uses a dedicated route so an N-1 API rejects
// a guarded action rather than ignoring its expectation fields. Retry with the
// same ContextWithIdempotencyKey after an ambiguous response.
func (c *Client) ApplyAppDebugRegressionAction(ctx context.Context, slug string, req GuardedDebugRegressionActionRequest) (DebugRegressionActionResponse, error) {
	var out DebugRegressionActionResponse
	err := c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/debug/regressions/actions", req, &out)
	return out, err
}
