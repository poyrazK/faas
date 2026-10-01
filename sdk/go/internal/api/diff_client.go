package api

import (
	"context"
	"net/url"
)

// Diff previews deployment intent without changing remote state. Blocking
// reports immutable placement conflicts and current new-app admission policy.
func (c *Client) Diff(ctx context.Context, slug string, req DiffRequest) (DiffResponse, error) {
	var out DiffResponse
	return out, c.do(ctx, "POST", "/v1/apps/"+url.PathEscape(slug)+"/diff", req, &out)
}
