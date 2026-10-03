package api

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

type GlobalCellResponse struct {
	ID         string `json:"id"`
	Region     string `json:"region"`
	APIBase    string `json:"api_base"`
	Enabled    bool   `json:"enabled"`
	Generation int64  `json:"generation"`
}

type PutGlobalCellRequest struct {
	Region  string `json:"region"`
	APIBase string `json:"api_base"`
	Enabled *bool  `json:"enabled"`
}

type CreateGlobalAppRequest struct {
	Slug string `json:"slug"`
}

type GlobalAppResponse struct {
	ID            string                    `json:"id"`
	Slug          string                    `json:"slug"`
	RoutingPolicy string                    `json:"routing_policy"`
	CreatedAt     time.Time                 `json:"created_at"`
	Targets       []GlobalAppTargetResponse `json:"targets"`
}

type GlobalAppTargetResponse struct {
	CellID            string    `json:"cell_id"`
	Region            string    `json:"region"`
	CellGeneration    int64     `json:"cell_generation"`
	RegionalAccountID string    `json:"regional_account_id"`
	RegionalAppID     string    `json:"regional_app_id"`
	RegionalAppSlug   string    `json:"regional_app_slug"`
	VerifiedAt        time.Time `json:"verified_at"`
	ResolutionStatus  string    `json:"resolution_status"`
}

// The key is supplied for one ownership probe and is never stored or returned.
type AttachGlobalAppTargetRequest struct {
	RegionalAppSlug string `json:"regional_app_slug"`
	RegionalAPIKey  string `json:"regional_api_key"`
}

type GlobalAppResolutionResponse struct {
	GlobalAppID     string `json:"global_app_id"`
	CellID          string `json:"cell_id"`
	Region          string `json:"region"`
	APIBase         string `json:"api_base"`
	RegionalAppID   string `json:"regional_app_id"`
	RegionalAppSlug string `json:"regional_app_slug"`
	CellGeneration  int64  `json:"cell_generation"`
}

// CellAppBindingResponse is an authenticated regional identity attestation,
// not a readiness or health response.
type CellAppBindingResponse struct {
	CellID    string `json:"cell_id"`
	Region    string `json:"region"`
	AccountID string `json:"account_id"`
	AppID     string `json:"app_id"`
	AppSlug   string `json:"app_slug"`
}

func (c *Client) ListGlobalCells(ctx context.Context) ([]GlobalCellResponse, error) {
	var out []GlobalCellResponse
	err := c.do(ctx, "GET", "/v1/global-cells", nil, &out)
	return out, err
}

func (c *Client) CreateGlobalApp(ctx context.Context, req CreateGlobalAppRequest) (GlobalAppResponse, error) {
	var out GlobalAppResponse
	err := c.do(ctx, "POST", "/v1/global-apps", req, &out)
	return out, err
}

func (c *Client) ListGlobalApps(ctx context.Context) ([]GlobalAppResponse, error) {
	var out []GlobalAppResponse
	err := c.do(ctx, "GET", "/v1/global-apps", nil, &out)
	return out, err
}

func (c *Client) GetGlobalApp(ctx context.Context, slug string) (GlobalAppResponse, error) {
	var out GlobalAppResponse
	err := c.do(ctx, "GET", "/v1/global-apps/"+url.PathEscape(slug), nil, &out)
	return out, err
}

func (c *Client) AttachGlobalAppTarget(ctx context.Context, slug, region string, req AttachGlobalAppTargetRequest) (GlobalAppTargetResponse, error) {
	var out GlobalAppTargetResponse
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	err := c.doWithClientAndIdempotencyKey(ctx, &client, "PUT", "/v1/global-apps/"+url.PathEscape(slug)+"/targets/"+url.PathEscape(region), req, &out, "")
	return out, err
}

func (c *Client) DetachGlobalAppTarget(ctx context.Context, slug, region string) error {
	return c.do(ctx, "DELETE", "/v1/global-apps/"+url.PathEscape(slug)+"/targets/"+url.PathEscape(region), nil, nil)
}

func (c *Client) ResolveGlobalApp(ctx context.Context, slug, region string) (GlobalAppResolutionResponse, error) {
	var out GlobalAppResolutionResponse
	err := c.do(ctx, "GET", "/v1/global-apps/"+url.PathEscape(slug)+"/targets/"+url.PathEscape(region)+"/resolve", nil, &out)
	return out, err
}

func (c *Client) GetCellAppBinding(ctx context.Context, slug string) (CellAppBindingResponse, error) {
	var out CellAppBindingResponse
	err := c.do(ctx, "GET", "/v1/cell/apps/"+url.PathEscape(slug)+"/binding", nil, &out)
	return out, err
}
