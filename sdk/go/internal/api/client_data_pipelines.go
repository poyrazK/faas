package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) CreateDataConnection(ctx context.Context, r CreateDataConnectionRequest) (DataConnection, error) {
	var out DataConnection
	err := c.do(ctx, http.MethodPost, "/v1/data/connections", r, &out)
	return out, err
}
func (c *Client) ListDataConnections(ctx context.Context) (DataConnectionList, error) {
	var out DataConnectionList
	err := c.do(ctx, http.MethodGet, "/v1/data/connections", nil, &out)
	return out, err
}
func (c *Client) RotateDataConnection(ctx context.Context, id string, r RotateDataConnectionRequest) (DataConnection, error) {
	var out DataConnection
	err := c.do(ctx, http.MethodPut, "/v1/data/connections/"+url.PathEscape(id), r, &out)
	return out, err
}
func (c *Client) DeleteDataConnection(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/data/connections/"+url.PathEscape(id), nil, nil)
}
func (c *Client) RequestDataConnectionCheck(ctx context.Context, id string, r RequestDataConnectionCheck) (DataConnectionCheck, error) {
	var out DataConnectionCheck
	err := c.do(ctx, http.MethodPost, "/v1/data/connections/"+url.PathEscape(id)+"/check", r, &out)
	return out, err
}
func (c *Client) GetDataConnectionCheck(ctx context.Context, id string) (DataConnectionCheck, error) {
	var out DataConnectionCheck
	err := c.do(ctx, http.MethodGet, "/v1/data/connections/"+url.PathEscape(id)+"/check", nil, &out)
	return out, err
}
func (c *Client) CreateDataPipeline(ctx context.Context, r CreateDataPipelineRequest) (DataPipeline, error) {
	var out DataPipeline
	err := c.do(ctx, http.MethodPost, "/v1/data/pipelines", r, &out)
	return out, err
}
func (c *Client) ListDataPipelines(ctx context.Context) (DataPipelineList, error) {
	var out DataPipelineList
	err := c.do(ctx, http.MethodGet, "/v1/data/pipelines", nil, &out)
	return out, err
}
func (c *Client) GetDataPipeline(ctx context.Context, id string) (DataPipeline, error) {
	var out DataPipeline
	err := c.do(ctx, http.MethodGet, "/v1/data/pipelines/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ControlDataPipeline(ctx context.Context, id string, r ControlDataPipelineRequest) (DataPipeline, error) {
	var out DataPipeline
	err := c.do(ctx, http.MethodPost, "/v1/data/pipelines/"+url.PathEscape(id)+"/control", r, &out)
	return out, err
}
