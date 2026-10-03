package api

import (
	"context"
	"net/url"
	"strconv"
)

func routeMonitorPath(slug string) string {
	return "/v1/apps/" + url.PathEscape(slug) + "/route-monitor"
}
func (c *Client) GetRouteMonitor(ctx context.Context, slug string) (RouteMonitorConfig, error) {
	var out RouteMonitorConfig
	err := c.do(ctx, "GET", routeMonitorPath(slug), nil, &out)
	return out, err
}
func (c *Client) SetRouteMonitor(ctx context.Context, slug string, req SetRouteMonitorRequest) (RouteMonitorConfig, error) {
	var out RouteMonitorConfig
	err := c.do(ctx, "PUT", routeMonitorPath(slug), req, &out)
	return out, err
}
func (c *Client) GetRouteMonitorReport(ctx context.Context, slug string) (RouteMonitorReport, error) {
	var out RouteMonitorReport
	err := c.do(ctx, "GET", routeMonitorPath(slug)+"/report", nil, &out)
	return out, err
}
func (c *Client) GetRouteMonitorIncident(ctx context.Context, slug, id string) (RouteMonitorIncident, error) {
	var out RouteMonitorIncident
	err := c.do(ctx, "GET", routeMonitorPath(slug)+"/incidents/"+url.PathEscape(id), nil, &out)
	return out, err
}
func (c *Client) ListRouteMonitorIncidents(ctx context.Context, slug string, limit int, before string) (RouteMonitorIncidentPage, error) {
	var out RouteMonitorIncidentPage
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		q.Set("before", before)
	}
	err := c.do(ctx, "GET", routeMonitorPath(slug)+"/incidents?"+q.Encode(), nil, &out)
	return out, err
}
