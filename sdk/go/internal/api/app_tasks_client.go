package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

func (c *Client) CreateAppTask(ctx context.Context, slug string, request CreateAppTaskRequest) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks"
	return out, c.do(ctx, http.MethodPost, path, request, &out)
}

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

func (c *Client) GetAppTask(ctx context.Context, slug, id string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

func (c *Client) CancelAppTask(ctx context.Context, slug, id string) (AppTaskResponse, error) {
	var out AppTaskResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/tasks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodDelete, path, nil, &out)
}
