package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// PreviewStatusResponse is the SDK read model used by preview-aware tooling.
// The latest deployment is optional because a preview can be provisioned
// before its first source-ref deployment is accepted by the queue.
type PreviewStatusResponse struct {
	App              AppResponse         `json:"app"`
	LatestDeployment *DeploymentResponse `json:"latest_deployment,omitempty"`
}

// GetPreviewSlug returns the dedicated preview resource in one request. The
// path-shaped name is the generated-SDK coverage contract.
func (c *Client) GetPreviewSlug(ctx context.Context, slug string) (PreviewResourceResponse, error) {
	var out PreviewResourceResponse
	path := "/v1/preview/" + url.PathEscape(slug)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetPreview is the ergonomic alias used by preview-aware tooling.
func (c *Client) GetPreview(ctx context.Context, slug string) (PreviewResourceResponse, error) {
	return c.GetPreviewSlug(ctx, slug)
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
