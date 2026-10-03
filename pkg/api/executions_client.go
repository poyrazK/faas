package api

import (
	"context"
	"io"
	"net/url"
	"strconv"
)

// StreamExecution opens the resumable SSE stream for one execution. The
// server sends ordered status/stdout/stderr/terminal events and closes after
// the terminal event. Pass after=0 to start at the beginning; callers may
// reconnect with the last event id they observed.
func (c *Client) StreamExecution(ctx context.Context, id string, after int64) (io.ReadCloser, error) {
	path := "/v1/executions/" + url.PathEscape(id) + "/events"
	if after > 0 {
		path += "?after=" + strconv.FormatInt(after, 10)
	}
	return c.stream(ctx, path)
}

// CreateExecution submits source and JSON input for one disposable, isolated
// run. The server seals the payload before it reaches the scheduler and
// returns an account-scoped receipt without echoing source or input.
func (c *Client) CreateExecution(ctx context.Context, req CreateExecutionRequest) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "POST", "/v1/executions", req, &out)
}

// GetExecutionCapabilities returns the authenticated account's Runs admission
// state, supported runtimes/profiles, and plan-resolved request limits.
func (c *Client) GetExecutionCapabilities(ctx context.Context) (ExecutionCapabilitiesResponse, error) {
	var out ExecutionCapabilitiesResponse
	return out, c.do(ctx, "GET", "/v1/executions/capabilities", nil, &out)
}

// GetExecution returns the current state or terminal result of one run.
func (c *Client) GetExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "GET", "/v1/executions/"+id, nil, &out)
}

// ListExecutions returns the newest account-scoped execution page. A zero
// limit or offset lets the server apply its defaults; status is optional.
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

// GetExecutionWorkflow returns workflow counts and terminal-run resource usage
// visible to the authenticated principal.
func (c *Client) GetExecutionWorkflow(ctx context.Context, workflowID string) (ExecutionWorkflowResponse, error) {
	var out ExecutionWorkflowResponse
	path := "/v1/execution-workflows/" + url.PathEscape(workflowID)
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CancelExecution requests cancellation. Cancellation is idempotent: queued
// runs become terminal immediately, while claimed runs are fenced during
// teardown by the scheduler.
func (c *Client) CancelExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "DELETE", "/v1/executions/"+id, nil, &out)
}
