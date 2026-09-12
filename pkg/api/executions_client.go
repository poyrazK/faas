package api

import "context"

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

// CancelExecution requests cancellation. Cancellation is idempotent: queued
// runs become terminal immediately, while claimed runs are fenced during
// teardown by the scheduler.
func (c *Client) CancelExecution(ctx context.Context, id string) (ExecutionResponse, error) {
	var out ExecutionResponse
	return out, c.do(ctx, "DELETE", "/v1/executions/"+id, nil, &out)
}
