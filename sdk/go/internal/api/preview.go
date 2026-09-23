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

// PreviewResourceResponse is the first-class preview read model. It keeps
// streamed logs and time-windowed metrics behind their native endpoints while
// making those endpoints, the effective app configuration, production
// baseline, artifact comparison, and expiration discoverable from one object.
type PreviewResourceResponse struct {
	App                  AppResponse                      `json:"app"`
	Parent               *AppResponse                     `json:"parent,omitempty"`
	LatestDeployment     *DeploymentResponse              `json:"latest_deployment,omitempty"`
	ProductionDeployment *DeploymentResponse              `json:"production_deployment,omitempty"`
	Changes              PreviewProductionChangesResponse `json:"changes_from_production"`
	Links                PreviewResourceLinksResponse     `json:"links"`
}

// PreviewProductionChangesResponse summarizes safe, non-secret differences
// between a preview and its production parent.
type PreviewProductionChangesResponse struct {
	ArtifactChanged            bool                    `json:"artifact_changed"`
	PreviewArtifact            PreviewArtifactResponse `json:"preview_artifact"`
	ProductionArtifact         PreviewArtifactResponse `json:"production_artifact"`
	ConfigurationChangedGroups []string                `json:"configuration_changed_groups"`
}

// PreviewArtifactResponse is the strongest available immutable identity for a
// deployment plus enough provenance for a human-readable comparison.
type PreviewArtifactResponse struct {
	DeploymentID string `json:"deployment_id,omitempty"`
	Revision     int    `json:"revision,omitempty"`
	Status       string `json:"status,omitempty"`
	ImageDigest  string `json:"image_digest,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	CommitSHA    string `json:"commit_sha,omitempty"`
	BuildID      string `json:"build_id,omitempty"`
}

// PreviewResourceLinksResponse points to the preview's public URL and native
// observability/configuration APIs. Logs remain SSE and metrics remain
// time-windowed instead of being embedded as stale snapshots.
type PreviewResourceLinksResponse struct {
	URL           string `json:"url"`
	Logs          string `json:"logs"`
	Metrics       string `json:"metrics"`
	Configuration string `json:"configuration"`
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
