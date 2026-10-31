package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Dashboard widget kinds (ADR-20261022173833).
const (
	DashboardWidgetAppMetric    = "app_metric"
	DashboardWidgetLogCount     = "log_count"
	DashboardWidgetCustomMetric = "custom_metric"
	DashboardWidgetSLO          = "slo"
)

// DashboardAppMetrics are the request metrics an app_metric widget reads.
var DashboardAppMetrics = []string{"request_count", "error_rate_pct", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms", "cold_start_pct"}

// DashboardWidget is one typed reference to a signal Gregale already serves.
// Which fields apply depends on Kind: app_metric and custom_metric use
// Metric, log_count uses Query (canonical log search filters), slo uses
// SLOID. Window defaults to DashboardDefaultWindow and is ignored by slo.
type DashboardWidget struct {
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	App    string `json:"app"`
	Metric string `json:"metric,omitempty"`
	Query  string `json:"query,omitempty"`
	SLOID  string `json:"slo_id,omitempty"`
	Window string `json:"window,omitempty"`
}

// Dashboard is a saved, named composition of widgets.
type Dashboard struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Widgets   []DashboardWidget `json:"widgets"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// DashboardRequest creates or replaces a dashboard.
type DashboardRequest struct {
	Name    string            `json:"name"`
	Widgets []DashboardWidget `json:"widgets"`
}

// DashboardsResponse is GET /v1/dashboards.
type DashboardsResponse struct {
	Dashboards []Dashboard `json:"dashboards"`
}

// ListDashboards runs GET /v1/dashboards.
func (c *Client) ListDashboards(ctx context.Context) (DashboardsResponse, error) {
	var out DashboardsResponse
	return out, c.do(ctx, "GET", "/v1/dashboards", nil, &out)
}

// CreateDashboard runs POST /v1/dashboards.
func (c *Client) CreateDashboard(ctx context.Context, req DashboardRequest) (Dashboard, error) {
	var out Dashboard
	return out, c.do(ctx, "POST", "/v1/dashboards", req, &out)
}

// GetDashboard runs GET /v1/dashboards/{id}.
func (c *Client) GetDashboard(ctx context.Context, id string) (Dashboard, error) {
	var out Dashboard
	return out, c.do(ctx, "GET", "/v1/dashboards/"+url.PathEscape(id), nil, &out)
}

// ReplaceDashboard runs PUT /v1/dashboards/{id}.
func (c *Client) ReplaceDashboard(ctx context.Context, id string, req DashboardRequest) (Dashboard, error) {
	var out Dashboard
	return out, c.do(ctx, "PUT", "/v1/dashboards/"+url.PathEscape(id), req, &out)
}

// DeleteDashboard runs DELETE /v1/dashboards/{id}.
func (c *Client) DeleteDashboard(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/v1/dashboards/"+url.PathEscape(id), nil, nil)
}

// DashboardData is GET /v1/dashboards/{id}/data: every widget resolved for
// its window. A widget that cannot be read carries Error and no value; the
// rest of the dashboard still resolves.
type DashboardData struct {
	ID      string                 `json:"id"`
	Name    string                 `json:"name"`
	AsOf    time.Time              `json:"as_of"`
	Widgets []DashboardWidgetValue `json:"widgets"`
}

// DashboardWidgetValue is one resolved widget. Value is the headline number
// in Unit; Detail adds context (log counts by level, SLO attainment); Series
// is the custom metric's points.
type DashboardWidgetValue struct {
	DashboardWidget
	Value  *float64         `json:"value,omitempty"`
	Unit   string           `json:"unit,omitempty"`
	Detail string           `json:"detail,omitempty"`
	Series []DashboardPoint `json:"series,omitempty"`
	Error  string           `json:"error,omitempty"`
}

// DashboardPoint is one time-series sample.
type DashboardPoint struct {
	At    time.Time `json:"at"`
	Value float64   `json:"value"`
}

// GetDashboardData runs GET /v1/dashboards/{id}/data.
func (c *Client) GetDashboardData(ctx context.Context, id string) (DashboardData, error) {
	var out DashboardData
	return out, c.do(ctx, "GET", "/v1/dashboards/"+url.PathEscape(id)+"/data", nil, &out)
}

// ErrDashboardsUnavailable is the FAAS_CUSTOM_DASHBOARDS_ENABLED gate's answer.
func ErrDashboardsUnavailable() *Problem {
	return NewProblem(http.StatusServiceUnavailable, "dashboards_unavailable", "Custom dashboards unavailable",
		"the custom dashboards preview is not enabled").WithHint(PreviewNotEnabledHint)
}

// ErrDashboardNotFound names a dashboard the account does not have.
func ErrDashboardNotFound() *Problem {
	return NewProblem(http.StatusNotFound, "dashboard_not_found", "Dashboard not found",
		"the account has no such dashboard").WithDocs(docsBase + "/dashboards").WithHint("list them with `gregale dashboards list`")
}

// ErrDashboardQuota rejects a new dashboard past the per-account cap.
func ErrDashboardQuota(limit, observed int) *Problem {
	p := NewProblem(http.StatusConflict, "dashboard_quota_exceeded", "Dashboard limit reached",
		fmt.Sprintf("the account has %d of %d dashboards", observed, limit))
	l, o := int64(limit), int64(observed)
	p.Limit, p.Observed = &l, &o
	return p.WithDocs(docsBase + "/dashboards").WithHint("delete one with `gregale dashboards delete`")
}

// ErrDashboardInvalid rejects a dashboard definition.
func ErrDashboardInvalid(detail string) *Problem {
	return NewProblem(http.StatusBadRequest, CodeValidation, "Invalid dashboard", detail).WithDocs(docsBase + "/dashboards")
}
