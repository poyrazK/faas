package api

import (
	"context"
	"net/http"
	"net/url"
)

func (c *Client) PlanDataPipelineSetup(ctx context.Context, r DataPipelineSetupRequest) (DataPipelineSetupPlan, error) {
	var out DataPipelineSetupPlan
	err := c.do(ctx, http.MethodPost, "/v1/data/pipeline-setups/plan", r, &out)
	return out, err
}
func (c *Client) CreateDataPipelineSetup(ctx context.Context, r DataPipelineSetupRequest) (DataPipelineSetup, error) {
	var out DataPipelineSetup
	err := c.do(ctx, http.MethodPost, "/v1/data/pipeline-setups", r, &out)
	return out, err
}
func (c *Client) ListDataPipelineSetups(ctx context.Context) (DataPipelineSetupList, error) {
	var out DataPipelineSetupList
	err := c.do(ctx, http.MethodGet, "/v1/data/pipeline-setups", nil, &out)
	return out, err
}
func (c *Client) GetDataPipelineSetup(ctx context.Context, id string) (DataPipelineSetup, error) {
	var out DataPipelineSetup
	err := c.do(ctx, http.MethodGet, "/v1/data/pipeline-setups/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ControlDataPipelineSetup(ctx context.Context, id string, r DataPipelineSetupControlRequest) (DataPipelineSetup, error) {
	var out DataPipelineSetup
	err := c.do(ctx, http.MethodPost, "/v1/data/pipeline-setups/"+url.PathEscape(id)+"/control", r, &out)
	return out, err
}
func (c *Client) ConfigureDataPipelineFreshness(ctx context.Context, id string, r DataPipelineFreshnessRequest) (DataPipeline, error) {
	var out DataPipeline
	err := c.do(ctx, http.MethodPost, "/v1/data/pipelines/"+url.PathEscape(id)+"/freshness", r, &out)
	return out, err
}
func (c *Client) ChangeDataPipelineIndexes(ctx context.Context, id string, r DataPipelineIndexRequest) (DataPipeline, error) {
	var out DataPipeline
	err := c.do(ctx, http.MethodPost, "/v1/data/pipelines/"+url.PathEscape(id)+"/indexes", r, &out)
	return out, err
}
