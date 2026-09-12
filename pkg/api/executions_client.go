package api

import (
	"context"
	"net/url"
	"strconv"
)

// CreateExecution submits source and JSON input for one disposable, isolated
// run. The server seals the payload before it reaches the scheduler and
// returns an account-scoped receipt without echoing source or input.
func (c *Client) CreateExecution(ctx context.Context, req CreateExecutionRequest) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "POST", "/v1/executions", req, &out)
}

// GetExecution returns the current state or terminal result of one run.
func (c *Client) GetExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "GET", "/v1/executions/"+id, nil, &out)
}

// ListExecutions returns the newest account-scoped execution page. A zero
// limit or offset lets the server apply its defaults; status is optional.
func (c *Client) ListExecutions(ctx context.Context, limit, offset int, status ExecutionStatus) (ExecutionListResponse, error) {
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
	path := "/v1/executions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out ExecutionListResponse
	return out, c.do(ctx, "GET", path, nil, &out)
}

// CancelExecution requests cancellation. Cancellation is idempotent: queued
// runs become terminal immediately, while claimed runs are fenced during
// teardown by the scheduler.
func (c *Client) CancelExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "DELETE", "/v1/executions/"+id, nil, &out)
}
