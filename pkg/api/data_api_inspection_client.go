package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) CreateDataAPIInspection(ctx context.Context, slug string, request CreateDataAPIInspectionRequest) (DataAPIInspectionResponse, error) {
	var out DataAPIInspectionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/data-api-inspections"
	err := c.do(ctx, http.MethodPost, path, request, &out)
	return out, err
}

func (c *Client) GetDataAPIInspection(ctx context.Context, slug, taskID string) (DataAPIInspectionResponse, error) {
	var out DataAPIInspectionResponse
	path := "/v1/apps/" + url.PathEscape(slug) + "/data-api-inspections/" + url.PathEscape(taskID)
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
