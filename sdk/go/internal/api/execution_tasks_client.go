package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func (c *PreviewClient) PublishExecutionTask(ctx context.Context, r PublishExecutionTaskRequest) (ExecutionTaskVersionResponse, error) {
	var out ExecutionTaskVersionResponse
	err := c.do(ctx, http.MethodPost, "/v1/run-tasks", r, &out)
	return out, err
}
func (c *PreviewClient) ListExecutionTasks(ctx context.Context, cursor string) (ExecutionTaskListResponse, error) {
	var out ExecutionTaskListResponse
	err := c.do(ctx, http.MethodGet, "/v1/run-tasks?cursor="+url.QueryEscape(cursor), nil, &out)
	return out, err
}
func (c *PreviewClient) GetExecutionTask(ctx context.Context, name string, version int) (ExecutionTaskVersionResponse, error) {
	var out ExecutionTaskVersionResponse
	err := c.do(ctx, http.MethodGet, executionTaskPath(name, version), nil, &out)
	return out, err
}
func (c *PreviewClient) RetireExecutionTask(ctx context.Context, name string, version int) error {
	return c.do(ctx, http.MethodDelete, executionTaskPath(name, version), nil, nil)
}
func (c *PreviewClient) InvokeExecutionTask(ctx context.Context, r InvokeExecutionTaskRequest, key string) (ExecutionResponse, error) {
	var out ExecutionResponse
	err := c.doWithIdempotencyKey(ctx, http.MethodPost, "/v1/run-tasks/invoke", r, &out, key)
	return out, err
}
func executionTaskPath(name string, version int) string {
	return "/v1/run-tasks/" + url.PathEscape(name) + "/versions/" + strconv.Itoa(version)
}
