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
