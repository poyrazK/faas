package api

import (
	"context"
	"io"
	"net/url"
	"strconv"
)

// StreamExecution opens the resumable SSE stream for one execution. Pass
// after=0 to start at the beginning; reconnects should use the latest cursor
// returned in an ExecutionEvent.
func (c *Client) StreamExecution(ctx context.Context, id string, after int64) (io.ReadCloser, error) {
	return c.StreamExecutionWithLimit(ctx, id, after, 0)
}

// StreamExecutionWithLimit is StreamExecution with an explicit replay batch
// size. A non-positive limit lets the server apply its default.
func (c *Client) StreamExecutionWithLimit(ctx context.Context, id string, after int64, limit int) (io.ReadCloser, error) {
	path := "/v1/executions/" + url.PathEscape(id) + "/events"
	query := url.Values{}
	if after > 0 {
		query.Set("after", strconv.FormatInt(after, 10))
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	return c.stream(ctx, path)
}

// CreateExecution submits source or an ephemeral files bundle for one
// isolated disposable run. The server seals the payload before dispatch and
// never echoes source or input in the returned receipt.
func (c *Client) CreateExecution(ctx context.Context, req CreateExecutionRequest) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "POST", "/v1/executions", req, &out)
}

// GetExecutionCapabilities returns the account's Runs admission contract and
// current plan limits. It does not report scheduler or runtime image readiness.
func (c *Client) GetExecutionCapabilities(ctx context.Context) (ExecutionCapabilitiesResponse, error) {
	var out ExecutionCapabilitiesResponse
	return out, c.do(ctx, "GET", "/v1/executions/capabilities", nil, &out)
}

// GetExecution returns the current state or terminal result for one run.
func (c *Client) GetExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "GET", "/v1/executions/"+url.PathEscape(id), nil, &out)
}

// ListExecutions returns an account-scoped page of disposable execution
// receipts. Zero limit and offset use the server defaults.
func (c *Client) ListExecutions(ctx context.Context, limit, offset int, status ExecutionStatus) (ExecutionListResponse, error) {
	return c.listExecutions(ctx, limit, offset, status, "")
}

// ListExecutionsForWorkflow returns the visible runs carrying workflowID.
func (c *Client) ListExecutionsForWorkflow(ctx context.Context, workflowID string, limit, offset int, status ExecutionStatus) (ExecutionListResponse, error) {
	return c.listExecutions(ctx, limit, offset, status, workflowID)
}

func (c *Client) listExecutions(ctx context.Context, limit, offset int, status ExecutionStatus, workflowID string) (ExecutionListResponse, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	if status != "" {
		query.Set("status", string(status))
	}
	if workflowID != "" {
		query.Set("workflow_id", workflowID)
	}
	path := "/v1/executions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out ExecutionListResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// GetExecutionWorkflow returns lifecycle and usage totals for the workflow
// visible to this credential's key family or account-wide scope.
func (c *Client) GetExecutionWorkflow(ctx context.Context, workflowID string) (ExecutionWorkflowResponse, error) {
	var out ExecutionWorkflowResponse
	path := "/v1/execution-workflows/" + url.PathEscape(workflowID)
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CancelExecution requests cancellation. Cancellation is idempotent and
// destroys a claimed VM during scheduler teardown.
func (c *Client) CancelExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "DELETE", "/v1/executions/"+url.PathEscape(id), nil, &out)
}

// CreateExecutionArtifactGrant creates a short-lived, single-use capability
// for one artifact. The response token is shown once and should be shared
// with the receiving agent over a secure channel.
func (c *Client) CreateExecutionArtifactGrant(ctx context.Context, executionID string, req CreateExecutionArtifactGrantRequest) (ExecutionArtifactGrantResponse, error) {
	var out ExecutionArtifactGrantResponse
	path := "/v1/executions/" + url.PathEscape(executionID) + "/artifact-grants"
	return out, c.do(ctx, "POST", path, req, &out)
}

// RevokeExecutionArtifactGrant prevents an unredeemed capability from being used.
func (c *Client) RevokeExecutionArtifactGrant(ctx context.Context, grantID string) (RevokeExecutionArtifactGrantResponse, error) {
	var out RevokeExecutionArtifactGrantResponse
	path := "/v1/execution-artifact-grants/" + url.PathEscape(grantID)
	return out, c.do(ctx, "DELETE", path, nil, &out)
}
