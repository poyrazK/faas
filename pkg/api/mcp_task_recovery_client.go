package api

import (
	"context"
	"net/url"
)

// RetryDeploymentMCPTaskHold schedules a compatibility hold retry without
// bypassing the worker's activation checks.
func (c *Client) RetryDeploymentMCPTaskHold(ctx context.Context, id string) (DeploymentResponse, error) {
	var out DeploymentResponse
	err := c.do(ctx, "POST", "/v1/deployments/"+url.PathEscape(id)+"/mcp-task-hold/retry", nil, &out)
	return out, err
}
