package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) CreateExecutionEnvironment(ctx context.Context, r CreateExecutionEnvironmentRequest) (ExecutionEnvironmentResponse, error) {
	var out ExecutionEnvironmentResponse
	err := c.do(ctx, http.MethodPost, "/v1/execution-environments", r, &out)
	return out, err
}
func (c *Client) GetExecutionEnvironment(ctx context.Context, id string) (ExecutionEnvironmentResponse, error) {
	var out ExecutionEnvironmentResponse
	err := c.do(ctx, http.MethodGet, "/v1/execution-environments/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ListExecutionEnvironments(ctx context.Context) ([]ExecutionEnvironmentResponse, error) {
	var out struct {
		Environments []ExecutionEnvironmentResponse `json:"environments"`
	}
	err := c.do(ctx, http.MethodGet, "/v1/execution-environments", nil, &out)
	return out.Environments, err
}
func (c *Client) DeleteExecutionEnvironment(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/execution-environments/"+url.PathEscape(id), nil, nil)
}

func (c *Client) ExecutionEnvironmentEvents(ctx context.Context, id string, after int64) (ExecutionEnvironmentEventsResponse, error) {
	var out ExecutionEnvironmentEventsResponse
	err := c.do(ctx, http.MethodGet, "/v1/execution-environments/"+url.PathEscape(id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
	return out, err
}

// WatchExecutionEnvironment polls a durable cursor until the build settles.
// Each page includes explicit retention gaps; interruption never cancels a build.
func (c *Client) WatchExecutionEnvironment(ctx context.Context, id string, after int64, visit func(ExecutionEnvironmentEventsResponse) error) error {
	for {
		page, err := c.ExecutionEnvironmentEvents(ctx, id, after)
		if err != nil {
			return err
		}
		if visit != nil {
			if err := visit(page); err != nil {
				return err
			}
		}
		after = page.NextSequence
		switch page.Status {
		case "ready", "failed", "retiring", "deleted":
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
