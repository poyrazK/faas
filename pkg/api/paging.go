package api

import "context"

// ListDeploymentsAll walks the next_before cursor on
// GET /v1/deployments until the server returns an empty cursor,
// returning every deployment in created_at DESC order. Useful for
// dashboards that want to render "every deploy ever" without forcing
// the customer to wire a loop.
//
// The server caps each page at 200 rows (handled by ListDeployments);
// this method requests max page size when walking.
//
// Cancelling ctx stops the walk at the next page boundary — the
// current page's rows are returned up to the cancellation point.
func (c *Client) ListDeploymentsAll(ctx context.Context) ([]DeploymentResponse, error) {
	var out []DeploymentResponse
	cursor := ""
	for {
		page, err := c.ListDeployments(ctx, cursor, 200)
		if err != nil {
			return out, err
		}
		out = append(out, page.Items...)
		if page.NextBefore == "" {
			return out, nil
		}
		cursor = page.NextBefore
		if err := ctx.Err(); err != nil {
			return out, err
		}
	}
}
