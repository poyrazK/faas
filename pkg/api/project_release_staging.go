package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// StageProjectReleaseRequest leases service capacity without publishing routes.
type StageProjectReleaseRequest struct {
	Manifest   CheckedProjectReleaseManifest `json:"manifest"`
	TTLSeconds int                           `json:"ttl_seconds"`
}

type ProjectReleaseStageMember struct {
	App          string `json:"app"`
	DeploymentID string `json:"deployment_id"`
	Desired      int    `json:"desired"`
	Ready        int    `json:"ready"`
}

type ProjectReleaseStage struct {
	ID          string                      `json:"id"`
	Project     string                      `json:"project"`
	Environment string                      `json:"environment"`
	Status      string                      `json:"status"`
	Reason      string                      `json:"reason,omitempty"`
	CreatedAt   time.Time                   `json:"created_at"`
	ExpiresAt   time.Time                   `json:"expires_at"`
	Members     []ProjectReleaseStageMember `json:"members"`
}

func (c *Client) StageCheckedProjectRelease(ctx context.Context, project, environment string, request StageProjectReleaseRequest) (ProjectReleaseStage, error) {
	var out ProjectReleaseStage
	err := c.do(ctx, http.MethodPost, projectReleaseSetsPath(project, environment)+"/staging", request, &out)
	return out, err
}
func (c *Client) GetProjectReleaseStage(ctx context.Context, project, environment, id string) (ProjectReleaseStage, error) {
	var out ProjectReleaseStage
	err := c.do(ctx, http.MethodGet, projectReleaseSetsPath(project, environment)+"/staging/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) CancelProjectReleaseStage(ctx context.Context, project, environment, id string) (ProjectReleaseStage, error) {
	var out ProjectReleaseStage
	err := c.do(ctx, http.MethodDelete, projectReleaseSetsPath(project, environment)+"/staging/"+url.PathEscape(id), nil, &out)
	return out, err
}
