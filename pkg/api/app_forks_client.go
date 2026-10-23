package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// CreateAppFork queues a production fork of the app's live deployment
// (ADR-732). The key needs secrets:read as well as deploy:write.
func (c *Client) CreateAppFork(ctx context.Context, slug string, req CreateAppForkRequest) (AppForkResponse, error) {
	var out AppForkResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/forks"
	return out, c.do(ctx, http.MethodPost, path, req, &out)
}

// ListAppForks returns the app's forks, newest first. A zero limit lets the
// server apply its default.
func (c *Client) ListAppForks(ctx context.Context, slug string, limit int) (AppForkListResponse, error) {
	path := "/v1/apps/" + url.PathEscape(slug) + "/forks"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var out AppForkListResponse
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// GetAppFork returns one fork.
func (c *Client) GetAppFork(ctx context.Context, slug, id string) (AppForkResponse, error) {
	var out AppForkResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/forks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodGet, path, nil, &out)
}

// CancelAppFork requests idempotent cancellation of one fork.
func (c *Client) CancelAppFork(ctx context.Context, slug, id string) (AppForkResponse, error) {
	var out AppForkResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/forks/" + url.PathEscape(id)
	return out, c.do(ctx, http.MethodDelete, path, nil, &out)
}
