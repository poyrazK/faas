package api

import (
	"context"
	"encoding/json"
	"net/url"
)

// CreateTenantWorkflowRun starts a workflow for an account-owned tenant that
// is actively linked to the app.
func (c *Client) CreateTenantWorkflowRun(ctx context.Context, tenantID, slug, workflowName string, input json.RawMessage) (WorkflowRunResponse, error) {
	var out WorkflowRunResponse
	path := "/v1/account/platform-tenants/" + url.PathEscape(tenantID) + "/apps/" + url.PathEscape(slug) + "/workflows/" + url.PathEscape(workflowName) + "/runs"
	return out, c.do(ctx, "POST", path, input, &out)
}

// CreatePlatformTenantSelfWorkflowRun starts a workflow using the tenant
// identity bound to the authenticated token.
func (c *Client) CreatePlatformTenantSelfWorkflowRun(ctx context.Context, slug, workflowName string, input json.RawMessage) (WorkflowRunResponse, error) {
	var out WorkflowRunResponse
	path := "/v1/platform-tenant-self/apps/" + url.PathEscape(slug) + "/workflows/" + url.PathEscape(workflowName) + "/runs"
	return out, c.do(ctx, "POST", path, input, &out)
}

// GetPlatformTenantSelfWorkflowRun reads a run owned by the authenticated
// platform tenant.
func (c *Client) GetPlatformTenantSelfWorkflowRun(ctx context.Context, id string) (WorkflowRunResponse, error) {
	var out WorkflowRunResponse
	path := "/v1/platform-tenant-self/workflows/runs/" + url.PathEscape(id)
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CancelPlatformTenantSelfWorkflowRun cancels an active run owned by the
// authenticated platform tenant.
func (c *Client) CancelPlatformTenantSelfWorkflowRun(ctx context.Context, id string) (WorkflowRunResponse, error) {
	var out WorkflowRunResponse
	path := "/v1/platform-tenant-self/workflows/runs/" + url.PathEscape(id) + "/cancel"
	return out, c.do(ctx, "POST", path, nil, &out)
}

// ResumePlatformTenantSelfWorkflowRun safely resumes eligible failed actions
// in a run owned by the authenticated platform tenant.
func (c *Client) ResumePlatformTenantSelfWorkflowRun(ctx context.Context, id string, body ResumeWorkflowRunRequest) (WorkflowRunResponse, error) {
	var out WorkflowRunResponse
	path := "/v1/platform-tenant-self/workflows/runs/" + url.PathEscape(id) + "/resume"
	return out, c.do(ctx, "POST", path, body, &out)
}
