package api

import (
	"context"
	"errors"
	"fmt"
)

// PreviewStatusResponse is the SDK read model used by preview-aware tooling.
// The latest deployment is optional because a preview can be provisioned
// before its first source-ref deployment is accepted by the queue.
type PreviewStatusResponse struct {
	App              AppResponse         `json:"app"`
	LatestDeployment *DeploymentResponse `json:"latest_deployment,omitempty"`
}

// GetPreviewStatus returns preview metadata and its newest deployment in one
// typed helper. A missing latest deployment is a valid empty state, while a
// non-preview app remains an error so callers cannot accidentally wait on
// production traffic.
func (c *Client) GetPreviewStatus(ctx context.Context, slug string) (PreviewStatusResponse, error) {
	var out PreviewStatusResponse
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		return out, err
	}
	if app.PreviewOfSlug == "" {
		return out, fmt.Errorf("%q is a production app, not a preview", slug)
	}
	out.App = app
	deployment, err := c.GetLatestAppDeployment(ctx, slug)
	if err == nil {
		out.LatestDeployment = &deployment
		return out, nil
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Status == 404 {
		return out, nil
	}
	return PreviewStatusResponse{}, err
}
