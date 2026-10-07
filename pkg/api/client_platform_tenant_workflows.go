package api

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"time"
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

// ListPlatformTenantSelfWorkflowRuns lists only runs for the authenticated
// tenant and the named app, with the same filters as the account run-history API.
func (c *Client) ListPlatformTenantSelfWorkflowRuns(ctx context.Context, slug string, opts WorkflowRunListOptions) (ListWorkflowRunsResponse, error) {
	var out ListWorkflowRunsResponse
	query := url.Values{}
	query.Set("limit", strconv.Itoa(opts.Limit))
	query.Set("offset", strconv.Itoa(opts.Offset))
	if opts.Status != "" {
		query.Set("status", opts.Status)
	}
	if opts.WorkflowName != "" {
		query.Set("workflow_name", opts.WorkflowName)
	}
	if opts.CreatedAfter != nil {
		query.Set("created_after", opts.CreatedAfter.UTC().Format(time.RFC3339Nano))
	}
	if opts.CreatedBefore != nil {
		query.Set("created_before", opts.CreatedBefore.UTC().Format(time.RFC3339Nano))
	}
	path := "/v1/platform-tenant-self/apps/" + url.PathEscape(slug) + "/workflows/runs?" + query.Encode()
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
