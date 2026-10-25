package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func operationWorkflowReadinessQueueQuery(opts OperationWorkflowReadinessQueueOptions, customer bool) string {
	q := url.Values{"scope": {opts.Scope}}
	for key, value := range map[string]string{"workflow": opts.Workflow, "target_operation": opts.TargetOperation, "owner": opts.Owner, "requirement": opts.Requirement} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if customer {
		q.Set("app_id", opts.AppID)
	} else if opts.TenantID != "" {
		q.Set("tenant_id", opts.TenantID)
	}
	if opts.Unassigned {
		q.Set("unassigned", "true")
	}
	if opts.Limit != 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	return "?" + q.Encode()
}
func (c *Client) ListAccountWorkflowReadinessQueue(ctx context.Context, slug string, opts OperationWorkflowReadinessQueueOptions) (OperationWorkflowReadinessQueueResponse, error) {
	var out OperationWorkflowReadinessQueueResponse
	err := c.doOperation(ctx, http.MethodGet, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/queue"+operationWorkflowReadinessQueueQuery(opts, false), nil, &out)
	return out, err
}
func (c *PreviewClient) ListPlatformTenantSelfWorkflowReadinessQueue(ctx context.Context, opts OperationWorkflowReadinessQueueOptions) (OperationWorkflowReadinessQueueResponse, error) {
	var out OperationWorkflowReadinessQueueResponse
	err := c.doOperation(ctx, http.MethodGet, "/v1/platform-tenant-self/workflow-readiness/queue"+operationWorkflowReadinessQueueQuery(opts, true), nil, &out)
	return out, err
}

func (c *Client) CompareAccountWorkflowReadiness(ctx context.Context, slug string, req OperationWorkflowReadinessComparisonRequest) (OperationWorkflowReadinessComparisonResponse, error) {
	var out OperationWorkflowReadinessComparisonResponse
	err := c.doOperation(ctx, http.MethodPost, "/v1/apps/"+url.PathEscape(slug)+"/workflow-readiness/compare", req, &out)
	return out, err
}
func (c *PreviewClient) ComparePlatformTenantSelfWorkflowReadiness(ctx context.Context, req OperationWorkflowReadinessComparisonRequest) (OperationWorkflowReadinessComparisonResponse, error) {
	var out OperationWorkflowReadinessComparisonResponse
	err := c.doOperation(ctx, http.MethodPost, "/v1/platform-tenant-self/workflow-readiness/compare", req, &out)
	return out, err
}
