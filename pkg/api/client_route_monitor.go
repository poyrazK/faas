package api

import (
	"context"
	"net/url"
	"strconv"
)

type RouteMonitorReadOptions struct{ CustomerDetails bool }

func routeMonitorDetailsPath(path string, opts RouteMonitorReadOptions) string {
	if opts.CustomerDetails {
		return path + "?customer_details=true"
	}
	return path
}

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
func (c *Client) PreviewRouteMonitor(ctx context.Context, slug string, req PreviewRouteMonitorRequest) (RouteMonitorPreview, error) {
	return c.PreviewRouteMonitorWithOptions(ctx, slug, req, RouteMonitorReadOptions{})
}
func (c *Client) PreviewRouteMonitorWithOptions(ctx context.Context, slug string, req PreviewRouteMonitorRequest, opts RouteMonitorReadOptions) (RouteMonitorPreview, error) {
	var out RouteMonitorPreview
	err := c.do(ctx, "POST", routeMonitorDetailsPath(routeMonitorPath(slug)+"/preview", opts), req, &out)
	return out, err
}
func (c *Client) GetRouteMonitorReport(ctx context.Context, slug string) (RouteMonitorReport, error) {
	return c.GetRouteMonitorReportWithOptions(ctx, slug, RouteMonitorReadOptions{})
}
func (c *Client) GetRouteMonitorReportWithOptions(ctx context.Context, slug string, opts RouteMonitorReadOptions) (RouteMonitorReport, error) {
	var out RouteMonitorReport
	err := c.do(ctx, "GET", routeMonitorDetailsPath(routeMonitorPath(slug)+"/report", opts), nil, &out)
	return out, err
}
func (c *Client) GetRouteMonitorIncident(ctx context.Context, slug, id string) (RouteMonitorIncident, error) {
	return c.GetRouteMonitorIncidentWithOptions(ctx, slug, id, RouteMonitorReadOptions{})
}
func (c *Client) GetRouteMonitorIncidentWithOptions(ctx context.Context, slug, id string, opts RouteMonitorReadOptions) (RouteMonitorIncident, error) {
	var out RouteMonitorIncident
	err := c.do(ctx, "GET", routeMonitorDetailsPath(routeMonitorPath(slug)+"/incidents/"+url.PathEscape(id), opts), nil, &out)
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
