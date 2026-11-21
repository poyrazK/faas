package api

import (
	"context"
	"net/http"
	"net/url"
)

// Product analytics client (ADR 20261117084505 §4).

func insightsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/insights"
}

// ProjectInsightEvents reads an event series; query holds event, from,
// until, interval, unit, by and repeated where=KEY:VALUE parameters.
func (c *Client) ProjectInsightEvents(ctx context.Context, project, environment string, query url.Values) (InsightEventSeries, error) {
	var out InsightEventSeries
	err := c.do(ctx, http.MethodGet, insightsPath(project, environment)+"/events?"+query.Encode(), nil, &out)
	return out, err
}

// ProjectInsightFunnel runs a funnel.
func (c *Client) ProjectInsightFunnel(ctx context.Context, project, environment string, req InsightFunnelRequest) (InsightFunnel, error) {
	var out InsightFunnel
	err := c.do(ctx, http.MethodPost, insightsPath(project, environment)+"/funnel", req, &out)
	return out, err
}

// ProjectInsightRetention runs a retention query.
func (c *Client) ProjectInsightRetention(ctx context.Context, project, environment string, req InsightRetentionRequest) (InsightRetention, error) {
	var out InsightRetention
	err := c.do(ctx, http.MethodPost, insightsPath(project, environment)+"/retention", req, &out)
	return out, err
}

// ProjectInsightRequests reads requests, server errors and p95 latency by
// flag value; query holds flag, from and until.
func (c *Client) ProjectInsightRequests(ctx context.Context, project, environment string, query url.Values) (InsightRequests, error) {
	var out InsightRequests
	err := c.do(ctx, http.MethodGet, insightsPath(project, environment)+"/requests?"+query.Encode(), nil, &out)
	return out, err
}

// Cohorts (ADR 20261119102741 §3).

func cohortsPath(project, environment string) string {
	return "/v1/projects/" + url.PathEscape(project) + "/environments/" + url.PathEscape(environment) + "/cohorts"
}

// ListProjectInsightCohorts lists an environment's cohorts.
func (c *Client) ListProjectInsightCohorts(ctx context.Context, project, environment string) (InsightCohortList, error) {
	var out InsightCohortList
	err := c.do(ctx, http.MethodGet, cohortsPath(project, environment), nil, &out)
	return out, err
}

// CreateProjectInsightCohort saves a cohort; Gregale computes its members
// on the next reconciler pass.
func (c *Client) CreateProjectInsightCohort(ctx context.Context, project, environment string, req InsightCohortRequest) (InsightCohort, error) {
	var out InsightCohort
	err := c.do(ctx, http.MethodPost, cohortsPath(project, environment), req, &out)
	return out, err
}

// GetProjectInsightCohort reads one cohort.
func (c *Client) GetProjectInsightCohort(ctx context.Context, project, environment, key string) (InsightCohort, error) {
	var out InsightCohort
	err := c.do(ctx, http.MethodGet, cohortsPath(project, environment)+"/"+url.PathEscape(key), nil, &out)
	return out, err
}

// DeleteProjectInsightCohort deletes a cohort; rules that target it stop
// matching its customers.
func (c *Client) DeleteProjectInsightCohort(ctx context.Context, project, environment, key string) error {
	return c.do(ctx, http.MethodDelete, cohortsPath(project, environment)+"/"+url.PathEscape(key), nil, nil)
}

// RefreshProjectInsightCohort asks for the cohort to be recomputed now.
func (c *Client) RefreshProjectInsightCohort(ctx context.Context, project, environment, key string) (InsightCohort, error) {
	var out InsightCohort
	err := c.do(ctx, http.MethodPost, cohortsPath(project, environment)+"/"+url.PathEscape(key)+"/refresh", nil, &out)
	return out, err
}
