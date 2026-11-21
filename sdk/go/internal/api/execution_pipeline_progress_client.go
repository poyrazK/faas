package api

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"time"
)

func (c *Client) ExecutionPipelineEvents(ctx context.Context, id string, after int64) (ExecutionPipelineEventsResponse, error) {
	var out ExecutionPipelineEventsResponse
	if after < 0 {
		return out, fmt.Errorf("pipeline cursor must be non-negative")
	}
	return out, c.do(ctx, "GET", "/v1/execution-pipelines/"+url.PathEscape(id)+"/events?after="+strconv.FormatInt(after, 10), nil, &out)
}
func (c *Client) StreamExecutionPipeline(ctx context.Context, id string, after int64) (io.ReadCloser, error) {
	if after < 0 {
		return nil, fmt.Errorf("pipeline cursor must be non-negative")
	}
	return c.stream(ctx, "/v1/execution-pipelines/"+url.PathEscape(id)+"/events?after="+strconv.FormatInt(after, 10))
}
func (c *Client) WatchExecutionPipeline(ctx context.Context, id string, after int64, visit func(ExecutionPipelineEventsResponse) error) error {
	for {
		page, err := c.ExecutionPipelineEvents(ctx, id, after)
		if err != nil {
			return err
		}
		if page.NextSequence < after {
			return fmt.Errorf("pipeline cursor regressed")
		}
		if visit != nil {
			if err = visit(page); err != nil {
				return err
			}
		}
		after = page.NextSequence
		if page.Terminal {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
func (c *Client) RerunExecutionPipeline(ctx context.Context, id string, request RerunExecutionPipelineRequest) (ManagedExecutionWorkflowResponse, error) {
	var out ManagedExecutionWorkflowResponse
	return out, c.do(ctx, "POST", "/v2/execution-pipelines/"+url.PathEscape(id)+"/reruns", request, &out)
}
