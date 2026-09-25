package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// CreateAppTask admits one manual command against the app's current live
// deployment. The returned receipt contains the immutable deployment selected
// by the server.
func (c *Client) CreateAppTask(ctx context.Context, slug string, req CreateAppTaskRequest) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// ListAppTasks returns the newest app-scoped task page. Zero limit and offset
// let the server apply its defaults.
func (c *Client) ListAppTasks(ctx context.Context, slug string, limit, offset int) (AppTaskListResponse, error) {
	query := url.Values{}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		query.Set("offset", strconv.Itoa(offset))
	}
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out AppTaskListResponse
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetAppTask returns the current state or terminal result of one app task.
func (c *Client) GetAppTask(ctx context.Context, slug, id string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetCronCommandRun returns the detailed receipt for one run belonging to a
// command cron, including captured output and retry metadata.
func (c *Client) GetCronCommandRun(ctx context.Context, cronID, runID string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/crons/" + url.PathEscape(cronID) + "/runs/" + url.PathEscape(runID)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// CancelAppTask requests idempotent cancellation of one app task.
func (c *Client) CancelAppTask(ctx context.Context, slug, id string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodDelete, path, nil, &out)
}
