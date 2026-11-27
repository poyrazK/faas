package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

func runBudgetPath(kind, ref string) string {
	return "/v1/run-controls/budgets/" + url.PathEscape(kind) + "/" + url.PathEscape(ref)
}
func runBindingPath(kind, id string) string {
	return "/v1/run-controls/bindings/" + url.PathEscape(kind) + "/" + url.PathEscape(id)
}
func (c *Client) GetRunBudget(ctx context.Context, kind, ref string) (RunBudgetPolicy, error) {
	var out RunBudgetPolicy
	err := c.do(ctx, http.MethodGet, runBudgetPath(kind, ref), nil, &out)
	return out, err
}
func (c *Client) SetRunBudget(ctx context.Context, kind, ref string, req SetRunBudgetRequest) (RunBudgetPolicy, error) {
	var out RunBudgetPolicy
	err := c.do(ctx, http.MethodPut, runBudgetPath(kind, ref), req, &out)
	return out, err
}
func (c *Client) GetRunAttributionBinding(ctx context.Context, kind, id string) (RunAttributionBinding, error) {
	var out RunAttributionBinding
	err := c.do(ctx, http.MethodGet, runBindingPath(kind, id), nil, &out)
	return out, err
}
func (c *Client) SetRunAttributionBinding(ctx context.Context, kind, id string, req SetRunAttributionBindingRequest) (RunAttributionBinding, error) {
	var out RunAttributionBinding
	err := c.do(ctx, http.MethodPut, runBindingPath(kind, id), req, &out)
	return out, err
}
func (c *Client) DeleteRunAttributionBinding(ctx context.Context, kind, id string, revision int64) error {
	return c.do(ctx, http.MethodDelete, runBindingPath(kind, id)+"?expected_revision="+strconv.FormatInt(revision, 10), nil, nil)
}
func (c *Client) ListRunHistory(ctx context.Context, f RunHistoryFilter) (RunHistoryResponse, error) {
	q := url.Values{}
	for k, v := range map[string]string{"task_name": f.TaskName, "customer_ref": f.CustomerRef, "agent_ref": f.AgentRef, "status": string(f.Status), "cursor": f.Cursor} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.TaskVersion != 0 {
		q.Set("task_version", strconv.Itoa(f.TaskVersion))
	}
	if !f.Since.IsZero() {
		q.Set("since", f.Since.UTC().Format(time.RFC3339Nano))
	}
	if !f.Until.IsZero() {
		q.Set("until", f.Until.UTC().Format(time.RFC3339Nano))
	}
	var out RunHistoryResponse
	err := c.do(ctx, http.MethodGet, "/v1/run-history?"+q.Encode(), nil, &out)
	return out, err
}
