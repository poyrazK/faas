package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type ManagedPostgresQueryInsights struct {
	DatabaseID      string                        `json:"database_id"`
	Enabled         bool                          `json:"enabled"`
	Since           time.Time                     `json:"since"`
	LastSampleAt    *time.Time                    `json:"last_sample_at,omitempty"`
	ObservedThrough *time.Time                    `json:"observed_through,omitempty"`
	ObservedSeconds int64                         `json:"observed_seconds"`
	Queries         []ManagedPostgresQueryInsight `json:"queries"`
}

type ManagedPostgresQueryInsight struct {
	QueryID          string    `json:"query_id"`
	BootAt           time.Time `json:"boot_at"`
	Calls            int64     `json:"calls"`
	TotalExecutionMS float64   `json:"total_execution_ms"`
	MeanExecutionMS  float64   `json:"mean_execution_ms"`
}

type SetManagedPostgresQueryInsightsRequest struct {
	Enabled bool `json:"enabled"`
}

func (c *Client) GetManagedPostgresQueryInsights(ctx context.Context, id string, since time.Duration) (ManagedPostgresQueryInsights, error) {
	return c.managedPostgresQueryInsights(ctx, "/v1/postgres", id, since, nil)
}

func (c *Client) GetOrgManagedPostgresQueryInsights(ctx context.Context, org, id string, since time.Duration) (ManagedPostgresQueryInsights, error) {
	return c.managedPostgresQueryInsights(ctx, orgPostgresPath(org), id, since, nil)
}

func (c *Client) SetManagedPostgresQueryInsights(ctx context.Context, id string, req SetManagedPostgresQueryInsightsRequest) (ManagedPostgresQueryInsights, error) {
	return c.managedPostgresQueryInsights(ctx, "/v1/postgres", id, 0, &req)
}

func (c *Client) SetOrgManagedPostgresQueryInsights(ctx context.Context, org, id string, req SetManagedPostgresQueryInsightsRequest) (ManagedPostgresQueryInsights, error) {
	return c.managedPostgresQueryInsights(ctx, orgPostgresPath(org), id, 0, &req)
}

func (c *Client) managedPostgresQueryInsights(ctx context.Context, prefix, id string, since time.Duration, req *SetManagedPostgresQueryInsightsRequest) (ManagedPostgresQueryInsights, error) {
	path := prefix + "/databases/" + url.PathEscape(id) + "/query-insights"
	var out ManagedPostgresQueryInsights
	if req != nil {
		err := c.do(ctx, http.MethodPut, path, req, &out)
		return out, err
	}
	if since != 0 {
		path += "?since_seconds=" + strconv.FormatInt(int64(since/time.Second), 10)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
