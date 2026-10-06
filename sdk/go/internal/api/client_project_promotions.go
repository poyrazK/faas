package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) GetProjectEnvironmentPromotionPreviewWithConfig(ctx context.Context, projectSlug, targetEnvironment, sourceEnvironment string, syncConfig bool) (ProjectEnvironmentPromotionPreviewResponse, error) {
	var out ProjectEnvironmentPromotionPreviewResponse
	query := url.Values{"from": []string{sourceEnvironment}}
	if syncConfig {
		query.Set("sync_config", "true")
	}
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promotion-preview?" + query.Encode()
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

func (c *Client) GetProjectEnvironmentPromotionPreview(ctx context.Context, projectSlug, targetEnvironment, sourceEnvironment string) (ProjectEnvironmentPromotionPreviewResponse, error) {
	return c.GetProjectEnvironmentPromotionPreviewWithConfig(ctx, projectSlug, targetEnvironment, sourceEnvironment, false)
}

func (c *Client) PromoteProjectEnvironment(ctx context.Context, projectSlug, targetEnvironment string, req PromoteProjectEnvironmentRequest) (ProjectEnvironmentPromotionResponse, error) {
	var out ProjectEnvironmentPromotionResponse
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promote"
	if req.RequireBindings {
		path += "-with-bindings"
	}
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

func (c *Client) PromoteProjectEnvironmentWithBindings(ctx context.Context, projectSlug, targetEnvironment string, req PromoteProjectEnvironmentRequest) (ProjectEnvironmentPromotionResponse, error) {
	req.RequireBindings = true
	return c.PromoteProjectEnvironment(ctx, projectSlug, targetEnvironment, req)
}

func (c *Client) GetProjectEnvironmentPromotionStatus(ctx context.Context, projectSlug, targetEnvironment, promotionID string) (ProjectEnvironmentPromotionStatusResponse, error) {
	var out ProjectEnvironmentPromotionStatusResponse
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promotions/" + url.PathEscape(promotionID)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

func (c *Client) ApproveProjectEnvironment(ctx context.Context, projectSlug, environmentSlug string, req CreateProjectEnvironmentApprovalRequest) (ProjectEnvironmentApprovalResponse, error) {
	var out ProjectEnvironmentApprovalResponse
	path := "/v1/projects/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/approvals"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}
