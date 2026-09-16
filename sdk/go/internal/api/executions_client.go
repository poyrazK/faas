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

// GetExecution returns the current state or terminal result for one run.
func (c *Client) GetExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "GET", "/v1/executions/"+url.PathEscape(id), nil, &out)
}

// ListExecutions returns an account-scoped page of disposable execution
// receipts. Zero limit and offset use the server defaults.
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

// CancelExecution requests cancellation. Cancellation is idempotent and
// destroys a claimed VM during scheduler teardown.
func (c *Client) CancelExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "DELETE", "/v1/executions/"+url.PathEscape(id), nil, &out)
}
