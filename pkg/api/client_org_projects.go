package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
)

func orgProjectPath(orgSlug string) string {
	return "/v1/orgs/" + url.PathEscape(orgSlug) + "/projects"
}

func (c *Client) DeleteOrgProjectDeploymentScopeExclusion(ctx context.Context, orgSlug, projectSlug, workloadSlug string) error {
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/exclusions/" + url.PathEscape(workloadSlug)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func orgProjectReleaseSetsPath(orgSlug, project, environment string) string {
	return orgProjectPath(orgSlug) + "/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/release-sets"
}

func (c *Client) GetActiveOrgProjectReleaseSet(ctx context.Context, orgSlug, project, environment string) (ProjectReleaseSetResponse, error) {
	var out ProjectReleaseSetResponse
	return out, c.do(ctx, http.MethodGet, orgProjectReleaseSetsPath(orgSlug, project, environment)+"/active", nil, &out)
}

func (c *Client) GetOrgProjectReleaseSet(ctx context.Context, orgSlug, project, environment, id string) (ProjectReleaseSetResponse, error) {
	var out ProjectReleaseSetResponse
	return out, c.do(ctx, http.MethodGet, orgProjectReleaseSetsPath(orgSlug, project, environment)+"/"+url.PathEscape(id), nil, &out)
}

func (c *Client) ListOrgProjectReleaseSets(ctx context.Context, orgSlug, project, environment, before string, limit int) (ProjectReleaseSetListResponse, error) {
	var out ProjectReleaseSetListResponse
	query := url.Values{}
	if before != "" {
		query.Set("before", before)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	return out, c.do(ctx, http.MethodGet, orgProjectReleaseSetsPath(orgSlug, project, environment)+"?"+query.Encode(), nil, &out)
}

// ApplyOrgProjectPlan addresses a project in the named organization.
func (c *Client) ApplyOrgProjectPlan(
	ctx context.Context, orgSlug string,
	planToken string,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (ApplyResponse, error) {
	return c.ApplyOrgProjectPlanWithBinding(ctx, orgSlug, planToken, source, sourceName, projectSlug, "", productionBranch,
		installID, only, exclude, persistExclude, noTriggers)
}

// ApplyOrgProjectPlanWithBinding addresses a project in the named organization.
func (c *Client) ApplyOrgProjectPlanWithBinding(
	ctx context.Context, orgSlug string,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (ApplyResponse, error) {
	return c.ApplyOrgProjectPlanWithBindingEnvironment(ctx, orgSlug, planToken, source, sourceName, projectSlug, repoFullName, productionBranch,
		installID, only, exclude, persistExclude, noTriggers, "")
}

// ApplyOrgProjectPlanWithBindingEnvironment addresses a project in the named organization.
func (c *Client) ApplyOrgProjectPlanWithBindingEnvironment(
	ctx context.Context, orgSlug string,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment string,
) (ApplyResponse, error) {
	return c.ApplyOrgProjectPlanWithBindingEnvironmentApproval(ctx, orgSlug, planToken, source, sourceName, projectSlug, repoFullName, productionBranch,
		installID, only, exclude, persistExclude, noTriggers, environment, "")
}

// ApplyOrgProjectPlanWithBindingEnvironmentApproval addresses a project in the named organization.
func (c *Client) ApplyOrgProjectPlanWithBindingEnvironmentApproval(
	ctx context.Context, orgSlug string,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment, approvalToken string,
	platformTenantRequired ...*bool,
) (ApplyResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment, approvalToken); err != nil {
		return ApplyResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	if err := writeProjectTenantPolicy(w, platformTenantRequired); err != nil {
		return ApplyResponse{}, fmt.Errorf("build tenant policy field: %w", err)
	}
	if err := w.Close(); err != nil {
		return ApplyResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}
	endpoint := c.baseURL + orgProjectPath(orgSlug)
	if planToken != "" {
		endpoint += "?plan_token=" + url.QueryEscape(planToken)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &b)
	if err != nil {
		return ApplyResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	// Multipart project applies bypass Client.do because the request body
	// carries a file. Preserve the caller's logical retry key just like the
	// JSON and single-app multipart paths; only mint a key for legacy callers
	// that did not attach one to the context.
	idempotencyKey := IdempotencyKeyFromContext(ctx)
	if idempotencyKey == "" {
		idempotencyKey = newUUIDv4()
	}
	req.Header.Set("Idempotency-Key", idempotencyKey)
	var out ApplyResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// ApproveOrgProjectEnvironment addresses a project in the named organization.
func (c *Client) ApproveOrgProjectEnvironment(ctx context.Context, orgSlug string, projectSlug, environmentSlug string, req CreateProjectEnvironmentApprovalRequest) (ProjectEnvironmentApprovalResponse, error) {
	var out ProjectEnvironmentApprovalResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/approvals"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// CreateOrgProjectEnvironment addresses a project in the named organization.
func (c *Client) CreateOrgProjectEnvironment(ctx context.Context, orgSlug string, projectSlug string, req CreateProjectEnvironmentRequest) (ProjectEnvironmentResponse, error) {
	if req.Full {
		return c.CreateFullProjectEnvironmentClone(ctx, projectSlug, req)
	}
	var out ProjectEnvironmentResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// CreateOrgProjectEnvironmentQualification addresses a project in the named organization.
func (c *Client) CreateOrgProjectEnvironmentQualification(ctx context.Context, orgSlug string, projectSlug, environmentSlug string, req CreateProjectEnvironmentQualificationRequest) (ProjectEnvironmentQualificationResponse, error) {
	var out ProjectEnvironmentQualificationResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/qualifications"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// DeleteOrgProject addresses a project in the named organization.
func (c *Client) DeleteOrgProject(ctx context.Context, orgSlug string, slug string) error {
	return c.do(ctx, http.MethodDelete, orgProjectPath(orgSlug)+"/"+url.PathEscape(slug), nil, nil)
}

// DeleteOrgProjectEnvironment addresses a project in the named organization.
func (c *Client) DeleteOrgProjectEnvironment(ctx context.Context, orgSlug string, projectSlug, environmentSlug string) error {
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug)
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// GetOrgProject addresses a project in the named organization.
func (c *Client) GetOrgProject(ctx context.Context, orgSlug string, slug string) (ProjectResponse, error) {
	var out ProjectResponse
	return out, c.do(ctx, http.MethodGet, orgProjectPath(orgSlug)+"/"+url.PathEscape(slug), nil, &out)
}

// GetOrgProjectEnvironment addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironment(ctx context.Context, orgSlug string, projectSlug, environmentSlug string) (ProjectEnvironmentResponse, error) {
	var out ProjectEnvironmentResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentApprovalStatus addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentApprovalStatus(ctx context.Context, orgSlug string, projectSlug, environmentSlug, approvalID string) (ProjectEnvironmentApprovalStatusResponse, error) {
	var out ProjectEnvironmentApprovalStatusResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/approvals/" + url.PathEscape(approvalID)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentConfig addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentConfig(ctx context.Context, orgSlug string, projectSlug, environmentSlug string) (ProjectEnvironmentConfigResponse, error) {
	var out ProjectEnvironmentConfigResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/config"
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentConfigDiff addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentConfigDiff(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, sourceEnvironment string) (ProjectEnvironmentConfigDiffResponse, error) {
	var out ProjectEnvironmentConfigDiffResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/config/diff?from=" + url.QueryEscape(sourceEnvironment)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentDiff addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentDiff(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, sourceEnvironment string) (ProjectEnvironmentDiffResponse, error) {
	var out ProjectEnvironmentDiffResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/diff?from=" + url.QueryEscape(sourceEnvironment)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentPromotionPreview addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentPromotionPreview(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, sourceEnvironment string) (ProjectEnvironmentPromotionPreviewResponse, error) {
	return c.GetOrgProjectEnvironmentPromotionPreviewWithConfig(ctx, orgSlug, projectSlug, targetEnvironment, sourceEnvironment, false)
}

// GetOrgProjectEnvironmentPromotionPreviewWithConfig addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentPromotionPreviewWithConfig(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, sourceEnvironment string, syncConfig bool) (ProjectEnvironmentPromotionPreviewResponse, error) {
	var out ProjectEnvironmentPromotionPreviewResponse
	query := url.Values{"from": []string{sourceEnvironment}}
	if syncConfig {
		query.Set("sync_config", "true")
	}
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promotion-preview?" + query.Encode()
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentPromotionStatus addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentPromotionStatus(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, promotionID string) (ProjectEnvironmentPromotionStatusResponse, error) {
	var out ProjectEnvironmentPromotionStatusResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promotions/" + url.PathEscape(promotionID)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentReleases addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentReleases(ctx context.Context, orgSlug string, projectSlug, environmentSlug string) (ProjectEnvironmentReleaseListResponse, error) {
	var out ProjectEnvironmentReleaseListResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/releases"
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetOrgProjectEnvironmentState addresses a project in the named organization.
func (c *Client) GetOrgProjectEnvironmentState(ctx context.Context, orgSlug string, projectSlug, environmentSlug string) (ProjectEnvironmentStateResponse, error) {
	var out ProjectEnvironmentStateResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/state"
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ListOrgProjectEnvironmentPromotions addresses a project in the named organization.
func (c *Client) ListOrgProjectEnvironmentPromotions(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, before string, limit int, sourceEnvironment, status string) (ProjectEnvironmentPromotionListResponse, error) {
	var out ProjectEnvironmentPromotionListResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promotions"
	query := url.Values{}
	if before != "" {
		query.Set("before", before)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if sourceEnvironment != "" {
		query.Set("from", sourceEnvironment)
	}
	if status != "" {
		query.Set("status", status)
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ListOrgProjectEnvironments addresses a project in the named organization.
func (c *Client) ListOrgProjectEnvironments(ctx context.Context, orgSlug string, projectSlug string) ([]ProjectEnvironmentResponse, error) {
	var out []ProjectEnvironmentResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments"
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// ListOrgProjects addresses a project in the named organization.
func (c *Client) ListOrgProjects(ctx context.Context, orgSlug string) ([]ProjectSummaryResponse, error) {
	var out []ProjectSummaryResponse
	return out, c.do(ctx, http.MethodGet, orgProjectPath(orgSlug), nil, &out)
}

// PreviewDeleteOrgProject addresses a project in the named organization.
func (c *Client) PreviewDeleteOrgProject(ctx context.Context, orgSlug string, slug string) (ProjectDeletePreviewResponse, error) {
	var out ProjectDeletePreviewResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(slug) + "/delete-preview"
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// PromoteOrgProjectEnvironment addresses a project in the named organization.
func (c *Client) PromoteOrgProjectEnvironment(ctx context.Context, orgSlug string, projectSlug, targetEnvironment string, req PromoteProjectEnvironmentRequest) (ProjectEnvironmentPromotionResponse, error) {
	var out ProjectEnvironmentPromotionResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promote"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// PromoteOrgProjectEnvironmentWithIdempotencyKey addresses a project in the named organization.
func (c *Client) PromoteOrgProjectEnvironmentWithIdempotencyKey(ctx context.Context, orgSlug string, projectSlug, targetEnvironment string, req PromoteProjectEnvironmentRequest, idempotencyKey string) (ProjectEnvironmentPromotionResponse, error) {
	var out ProjectEnvironmentPromotionResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promote"
	return out, c.doWithIdempotencyKey(ctx, http.MethodPost, path, req, &out, idempotencyKey)
}

// PublishOrgProjectReleaseSet addresses a project in the named organization.
func (c *Client) PublishOrgProjectReleaseSet(ctx context.Context, orgSlug string, projectSlug, environmentSlug string, req PublishProjectReleaseSetRequest) (ProjectReleaseSetResponse, error) {
	var out ProjectReleaseSetResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/release-sets"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// RollbackOrgProjectEnvironmentPromotion addresses a project in the named organization.
func (c *Client) RollbackOrgProjectEnvironmentPromotion(ctx context.Context, orgSlug string, projectSlug, targetEnvironment, promotionID string) (ProjectEnvironmentPromotionStatusResponse, error) {
	var out ProjectEnvironmentPromotionStatusResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(targetEnvironment) + "/promotions/" + url.PathEscape(promotionID) + "/rollback"
	return out, c.do(ctx, http.MethodPost, path, nil, &out)
}

// ScanOrgProject addresses a project in the named organization.
func (c *Client) ScanOrgProject(
	ctx context.Context, orgSlug string,
	source io.Reader, sourceName, projectSlug, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (PlanResponse, error) {
	return c.ScanOrgProjectWithBinding(ctx, orgSlug, source, sourceName, projectSlug, "", productionBranch,
		installID, only, exclude, persistExclude, noTriggers)
}

// ScanOrgProjectWithBinding addresses a project in the named organization.
func (c *Client) ScanOrgProjectWithBinding(
	ctx context.Context, orgSlug string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
) (PlanResponse, error) {
	return c.ScanOrgProjectWithBindingEnvironment(ctx, orgSlug, source, sourceName, projectSlug, repoFullName, productionBranch,
		installID, only, exclude, persistExclude, noTriggers, "")
}

// ScanOrgProjectWithBindingEnvironment addresses a project in the named organization.
func (c *Client) ScanOrgProjectWithBindingEnvironment(
	ctx context.Context, orgSlug string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment string,
	platformTenantRequired ...*bool,
) (PlanResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment); err != nil {
		return PlanResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	if err := writeProjectTenantPolicy(w, platformTenantRequired); err != nil {
		return PlanResponse{}, fmt.Errorf("build tenant policy field: %w", err)
	}
	if err := w.Close(); err != nil {
		return PlanResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+orgProjectPath(orgSlug)+"/scan", &b)
	if err != nil {
		return PlanResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var out PlanResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// UpdateOrgProject addresses a project in the named organization.
func (c *Client) UpdateOrgProject(ctx context.Context, orgSlug string, slug string, req UpdateProjectRequest) (ProjectResponse, error) {
	var out ProjectResponse
	return out, c.do(ctx, http.MethodPatch, orgProjectPath(orgSlug)+"/"+url.PathEscape(slug), req, &out)
}

// UpdateOrgProjectEnvironment addresses a project in the named organization.
func (c *Client) UpdateOrgProjectEnvironment(ctx context.Context, orgSlug string, projectSlug, environmentSlug string, req UpdateProjectEnvironmentRequest) (ProjectEnvironmentResponse, error) {
	var out ProjectEnvironmentResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug)
	return out, c.do(ctx, http.MethodPatch, path, req, &out)
}

// UpdateOrgProjectEnvironmentConfig addresses a project in the named organization.
func (c *Client) UpdateOrgProjectEnvironmentConfig(ctx context.Context, orgSlug string, projectSlug, environmentSlug string, req UpdateProjectEnvironmentConfigRequest) (ProjectEnvironmentConfigResponse, error) {
	var out ProjectEnvironmentConfigResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/config"
	return out, c.do(ctx, http.MethodPut, path, req, &out)
}

// UpdateOrgProjectEnvironmentPolicies addresses a project in the named organization.
func (c *Client) UpdateOrgProjectEnvironmentPolicies(ctx context.Context, orgSlug string, projectSlug, environmentSlug, workloadSlug string, req UpdateProjectEnvironmentEdgePolicyRequest) (ProjectEnvironmentEdgePolicyResponse, error) {
	var out ProjectEnvironmentEdgePolicyResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/workloads/" + url.PathEscape(workloadSlug) + "/policies"
	return out, c.do(ctx, http.MethodPut, path, req, &out)
}

// UpdateOrgProjectEnvironmentRoutes addresses a project in the named organization.
func (c *Client) UpdateOrgProjectEnvironmentRoutes(ctx context.Context, orgSlug string, projectSlug, environmentSlug, workloadSlug string, req UpdateProjectEnvironmentRoutePolicyRequest) (ProjectEnvironmentRoutePolicyResponse, error) {
	var out ProjectEnvironmentRoutePolicyResponse
	path := orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/environments/" + url.PathEscape(environmentSlug) + "/workloads/" + url.PathEscape(workloadSlug) + "/routes"
	return out, c.do(ctx, http.MethodPut, path, req, &out)
}

// ScanOrgProjectDeployment uses the existing project namespace selected by its organization URL.
func (c *Client) ScanOrgProjectDeployment(
	ctx context.Context, orgSlug string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment string,
	platformTenantRequired ...*bool,
) (PlanResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment); err != nil {
		return PlanResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	if err := writeProjectTenantPolicy(w, platformTenantRequired); err != nil {
		return PlanResponse{}, fmt.Errorf("build tenant policy field: %w", err)
	}
	if err := w.Close(); err != nil {
		return PlanResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+orgProjectPath(orgSlug)+"/"+url.PathEscape(projectSlug)+"/scan", &b)
	if err != nil {
		return PlanResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	var out PlanResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}

// DeployOrgProject uses the existing project namespace selected by its organization URL.
func (c *Client) DeployOrgProject(
	ctx context.Context, orgSlug string,
	planToken string,
	source io.Reader, sourceName, projectSlug, repoFullName, productionBranch string,
	installID int64, only, exclude []string, persistExclude, noTriggers bool,
	environment, approvalToken string,
	platformTenantRequired ...*bool,
) (ApplyResponse, error) {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	if err := writeProjectMultipartFields(w, source, sourceName, projectSlug, repoFullName, productionBranch, installID, only, exclude, persistExclude, noTriggers, environment, approvalToken); err != nil {
		return ApplyResponse{}, fmt.Errorf("build multipart: %w", err)
	}
	if err := writeProjectTenantPolicy(w, platformTenantRequired); err != nil {
		return ApplyResponse{}, fmt.Errorf("build tenant policy field: %w", err)
	}
	if err := w.Close(); err != nil {
		return ApplyResponse{}, fmt.Errorf("close multipart writer: %w", err)
	}
	endpoint := c.baseURL + orgProjectPath(orgSlug) + "/" + url.PathEscape(projectSlug) + "/deploy"
	if planToken != "" {
		endpoint += "?plan_token=" + url.QueryEscape(planToken)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, &b)
	if err != nil {
		return ApplyResponse{}, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	// Multipart project applies bypass Client.do because the request body
	// carries a file. Preserve the caller's logical retry key just like the
	// JSON and single-app multipart paths; only mint a key for legacy callers
	// that did not attach one to the context.
	idempotencyKey := IdempotencyKeyFromContext(ctx)
	if idempotencyKey == "" {
		idempotencyKey = newUUIDv4()
	}
	req.Header.Set("Idempotency-Key", idempotencyKey)
	var out ApplyResponse
	return out, c.doReq(c.uploadHTTP(), req, &out)
}
