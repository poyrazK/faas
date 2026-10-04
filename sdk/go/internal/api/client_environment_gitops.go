package api

import (
	"context"
	"net/http"
	"net/url"
)

func environmentGitOpsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/gitops"
}

func (c *Client) CreateEnvironmentGitSource(ctx context.Context, project, environment string, request CreateEnvironmentGitSourceRequest) (EnvironmentGitSource, error) {
	var out EnvironmentGitSource
	err := c.do(ctx, http.MethodPost, environmentGitOpsPath(project, environment)+"/source", request, &out)
	return out, err
}

func (c *Client) GetEnvironmentGitOps(ctx context.Context, project, environment string) (EnvironmentGitOpsStatusResponse, error) {
	var out EnvironmentGitOpsStatusResponse
	err := c.do(ctx, http.MethodGet, environmentGitOpsPath(project, environment), nil, &out)
	return out, err
}

func (c *Client) UpdateEnvironmentGitSource(ctx context.Context, project, environment string, request EnvironmentGitSourceUpdate) (EnvironmentGitSource, error) {
	var out EnvironmentGitSource
	err := c.do(ctx, http.MethodPatch, environmentGitOpsPath(project, environment)+"/source", request, &out)
	return out, err
}

func (c *Client) PreviewEnvironmentGitRevision(ctx context.Context, project, environment string, request PreviewEnvironmentGitRevisionRequest) (PreviewEnvironmentGitRevisionResponse, error) {
	var out PreviewEnvironmentGitRevisionResponse
	err := c.do(ctx, http.MethodPost, environmentGitOpsPath(project, environment)+"/revisions/preview", request, &out)
	return out, err
}

func (c *Client) ApproveEnvironmentGitRevision(ctx context.Context, project, environment string, request ApproveEnvironmentGitRevisionRequest) (ApproveEnvironmentGitRevisionResponse, error) {
	var out ApproveEnvironmentGitRevisionResponse
	err := c.do(ctx, http.MethodPost, environmentGitOpsPath(project, environment)+"/revisions/approve", request, &out)
	return out, err
}

func (c *Client) PreviewEnvironmentGitOpsAdoption(ctx context.Context, project, environment string) (EnvironmentGitOpsPlan, error) {
	var out EnvironmentGitOpsPlan
	err := c.do(ctx, http.MethodGet, environmentGitOpsPath(project, environment)+"/adoption-preview", nil, &out)
	return out, err
}

func (c *Client) AdoptEnvironmentGitOps(ctx context.Context, project, environment string, request AdoptEnvironmentGitOpsRequest) (AdoptEnvironmentGitOpsResponse, error) {
	var out AdoptEnvironmentGitOpsResponse
	err := c.do(ctx, http.MethodPost, environmentGitOpsPath(project, environment)+"/adopt", request, &out)
	return out, err
}

func (c *Client) CreateEnvironmentGitOpsOverride(ctx context.Context, project, environment string, request EnvironmentGitOpsOverrideRequest) (EnvironmentGitOpsOverrideRequest, error) {
	var out EnvironmentGitOpsOverrideRequest
	err := c.do(ctx, http.MethodPost, environmentGitOpsPath(project, environment)+"/overrides", request, &out)
	return out, err
}

func (c *Client) RemoveEnvironmentGitOpsOverride(ctx context.Context, project, environment string, request RemoveEnvironmentGitOpsOverrideRequest) error {
	return c.do(ctx, http.MethodDelete, environmentGitOpsPath(project, environment)+"/overrides", request, nil)
}
