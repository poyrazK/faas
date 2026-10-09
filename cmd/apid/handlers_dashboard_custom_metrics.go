package main

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

const customMetricSparkWidth, customMetricSparkHeight = 240, 36

// renderCustomMetricsDashboard serves GET /dashboard/apps/{slug}/custom-metrics
// (ADR-745): each pushed metric with its latest value and 24h history.
func (s *server) renderCustomMetricsDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	data, err := s.dashboardCustomMetricsData(r, app)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	page := dashboard.Page{Title: "Custom metrics", Body: "custom_metrics", Account: dashboardAccountView(acct, 0), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardCustomMetricsData(r *http.Request, app state.App) (dashboard.CustomMetricsData, error) {
	data := dashboard.CustomMetricsData{Enabled: s.customMetricHistoryEnabled, AppSlug: app.Slug, MaxMetrics: api.MaxCustomMetricsPerApp}
	if !data.Enabled {
		return data, nil
	}
	rows, err := s.store.ListCustomMetrics(r.Context(), app.ID)
	if err != nil {
		return data, err
	}
	now := time.Now().UTC()
	freshness := time.Duration(api.CustomMetricFreshnessSeconds) * time.Second
	for _, row := range rows {
		item := dashboard.CustomMetricRow{Name: row.Name, Value: row.Value, ObservedAt: row.ObservedAt.UTC(), Fresh: now.Sub(row.ObservedAt) <= freshness}
		series := s.customMetricSeries(r.Context(), app.ID, row.Name, api.CustomMetricSeriesDefaultRange, now)
		switch {
		case series.Source != appmetrics.SourcePrometheus:
			item.Note = "history unavailable right now"
		case len(series.Points) == 0:
			item.Note = "no values pushed in the last 24 hours"
		default:
			points := make([]appmetrics.SparklinePoint, 0, len(series.Points))
			for _, p := range series.Points {
				points = append(points, appmetrics.SparklinePoint{Time: p.At, Value: p.Value})
			}
			item.Spark = views.RenderAreaSparkline(points, customMetricSparkWidth, customMetricSparkHeight, "#1a4480", "")
		}
		data.Metrics = append(data.Metrics, item)
	}
	return data, nil
}
