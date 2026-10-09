package api

import (
	"context"
	"net/url"
)

func (c *Client) GetDeploymentRuntime(ctx context.Context, id string) (DeploymentRuntimeResponse, error) {
	var out DeploymentRuntimeResponse
	err := c.do(ctx, "GET", "/v1/deployments/"+url.PathEscape(id)+"/runtime", nil, &out)
	return out, err
}
func (c *Client) PreviewRuntimeUpgrade(ctx context.Context, id, target string) (RuntimeUpgradePreviewResponse, error) {
	var out RuntimeUpgradePreviewResponse
	err := c.do(ctx, "GET", "/v1/deployments/"+url.PathEscape(id)+"/runtime/upgrade-preview?"+url.Values{"target": {target}}.Encode(), nil, &out)
	return out, err
}
