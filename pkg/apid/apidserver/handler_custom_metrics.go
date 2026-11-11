// Custom application metrics (ADR-202).
//
//	PUT    /v1/apps/{slug}/custom-metrics/{name}
//	GET    /v1/apps/{slug}/custom-metrics
//	DELETE /v1/apps/{slug}/custom-metrics/{name}
//
// The push is the only write surface in the scaling path a customer's own
// infrastructure calls directly, which is why the name lives in the URL: it
// makes the write idempotent by construction (PUT of the same name twice is
// one row), and it keeps the body to the single number that actually varies.
//
// "custom-metrics" and not "metrics": GET /v1/apps/{slug}/metrics already
// serves the per-app Prometheus rollup (ADR-042), which is what the PLATFORM
// measured about the app. These are the numbers only the app's own domain
// knows. Colliding the two paths would also have been a routing panic.
package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// putCustomMetric handles PUT /v1/apps/{slug}/custom-metrics/{name}.
//
// The caller is frequently NOT the app: a cron, a database trigger, or the
// customer's own infrastructure watching their own queue. That is the point
// of a push rather than a scrape — a parked app has no process, and a
// scale-to-zero platform whose custom signal requires a running instance
// cannot scale from zero on it.
// customMetricLimit is the app's cap on distinct custom metric names
// (ADR-20261108160000): its resource owner's plan, so an organization's app
// follows the organization's plan, falling back to the caller's plan when
// the owner cannot be read or its plan has no custom metrics.
func (s *server) customMetricLimit(ctx context.Context, fallback api.Plan, app state.App) int {
	return s.customMetricCapsFor(ctx, fallback, app).names
}

// customMetricCaps are an app's custom metric caps: distinct names and
// labelled series (ADR-20261108160000 slice C).
type customMetricCaps struct{ names, series int }

func (s *server) customMetricCapsFor(ctx context.Context, fallback api.Plan, app state.App) customMetricCaps {
	plan := fallback
	if owner, err := state.AppResourceAccount(ctx, s.store, app); err == nil && owner.Plan.CustomMetricsPerApp() > 0 {
		plan = owner.Plan
	}
	return customMetricCaps{names: plan.CustomMetricsPerApp(), series: plan.CustomMetricSeriesPerApp()}
}

func (s *server) putCustomMetric(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.CustomMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanCustomMetricsNotAllowed(acct.Plan))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	name := r.PathValue("name")
	if problem := api.ValidateCustomMetricName(name); problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	var req api.CustomMetricRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<12)).Decode(&req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid custom metric", "body must be a JSON object with a numeric `value`."))
		return
	}
	if problem := api.ValidateCustomMetricValue(req.Value); problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	// The store enforces the distinct-name cap inside the insert, not with
	// a preceding count: two concurrent pushes of two NEW names would both
	// pass a check-then-insert against a cap of one.
	caps := s.customMetricCapsFor(r.Context(), acct.Plan, app)
	err := s.savePushedCustomMetric(r.Context(), app.ID, name, req.Value, caps)
	if errors.Is(err, state.ErrCustomMetricLimit) || errors.Is(err, state.ErrCustomMetricSeriesLimit) {
		api.WriteProblem(w, api.ErrCustomMetricLimitReached(caps.names))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "save custom metric",
			"Gregale could not save this custom metric.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// savePushedCustomMetric stores one pushed gauge. For a metric with labelled
// series (ADR-20261108160000 slice C) the push is its unlabelled series, so
// the aggregate stays the series' maximum instead of being overwritten.
func (s *server) savePushedCustomMetric(ctx context.Context, appID, name string, value float64, caps customMetricCaps) error {
	now := time.Now()
	if series, ok := s.store.(state.CustomMetricSeriesStore); ok && caps.series > 0 {
		rows, err := s.store.ListCustomMetrics(ctx, appID)
		if err != nil {
			return fmt.Errorf("list custom metrics: %w", err)
		}
		for _, row := range rows {
			if row.Name == name && row.Labelled {
				return series.PutCustomMetricSeries(ctx, appID, state.CustomMetricSeriesWrite{
					Name: name, Kind: state.CustomMetricKindGauge, Value: value, ObservedAt: now,
				}, state.CustomMetricSeriesLimits{
					Names: caps.names, SeriesPerMetric: api.CustomMetricMaxSeriesPerMetric, SeriesPerApp: caps.series,
					GaugeFreshness: api.CustomMetricFreshnessSeconds * time.Second,
				})
			}
		}
	}
	return s.store.PutCustomMetric(ctx, appID, name, value, now, caps.names)
}

// listCustomMetrics handles GET /v1/apps/{slug}/metrics.
//
// Returns the raw stored rows including observed_at, deliberately WITHOUT
// filtering stale ones. An operator debugging "why isn't my custom target
// scaling" needs to see that the value is old — hiding an expired row would
// make a dead pusher look like a missing one.
func (s *server) listCustomMetrics(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := s.store.ListCustomMetrics(r.Context(), app.ID)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list custom metrics",
			"Gregale could not load custom metrics for this app.",
			"Refresh the page or retry the request in a moment.", err)
		return
	}
	series, err := s.customMetricSeriesByName(r.Context(), app.ID, rows)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list custom metric series",
			"Gregale could not load custom metrics for this app.",
			"Refresh the page or retry the request in a moment.", err)
		return
	}
	caps := s.customMetricCapsFor(r.Context(), acct.Plan, app)
	out := api.CustomMetricListResponse{
		Metrics:    make([]api.CustomMetricResponse, 0, len(rows)),
		FreshnessS: api.CustomMetricFreshnessSeconds,
		MaxMetrics: caps.names,
		MaxSeries:  caps.series,
	}
	now := time.Now()
	freshness := time.Duration(api.CustomMetricFreshnessSeconds) * time.Second
	for _, row := range rows {
		out.Metrics = append(out.Metrics, api.CustomMetricResponse{
			Name:       row.Name,
			Value:      row.Value,
			ObservedAt: row.ObservedAt.UTC(),
			Kind:       row.Kind,
			// Stale is computed here rather than left to the client so
			// the API and the scheduler cannot disagree about which rows
			// are driving scaling.
			Stale:  now.Sub(row.ObservedAt) > freshness,
			Series: customMetricSeriesValues(series[row.Name], now, freshness),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// customMetricSeriesByName reads the app's labelled series, grouped by
// metric name, only when some metric is labelled.
func (s *server) customMetricSeriesByName(ctx context.Context, appID string, rows []state.CustomMetric) (map[string][]state.CustomMetricSeries, error) {
	store, ok := s.store.(state.CustomMetricSeriesStore)
	if !ok || !slices.ContainsFunc(rows, func(m state.CustomMetric) bool { return m.Labelled }) {
		return nil, nil
	}
	all, err := store.ListCustomMetricSeries(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := map[string][]state.CustomMetricSeries{}
	for _, row := range all {
		out[row.Name] = append(out[row.Name], row)
	}
	return out, nil
}

func customMetricSeriesValues(rows []state.CustomMetricSeries, now time.Time, freshness time.Duration) []api.CustomMetricSeriesValue {
	if len(rows) == 0 {
		return nil
	}
	out := make([]api.CustomMetricSeriesValue, 0, len(rows))
	for _, row := range rows {
		labels := row.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		out = append(out, api.CustomMetricSeriesValue{
			Key: row.SeriesKey, Labels: labels, Value: row.Value,
			ObservedAt: row.ObservedAt.UTC(), Stale: now.Sub(row.ObservedAt) > freshness,
		})
	}
	return out
}

// deleteCustomMetric handles DELETE /v1/apps/{slug}/custom-metrics/{name}.
//
// Deleting a name that does not exist returns 204, not 404: the caller's
// intent is "this metric is gone", which is already true, and a 404 would
// make a retry of a successful delete look like a failure.
func (s *server) deleteCustomMetric(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if err := s.store.DeleteCustomMetric(r.Context(), app.ID, r.PathValue("name")); err != nil {
		writeCustomerInternalProblem(w, r, s.log, "delete custom metric",
			"Gregale could not delete this custom metric.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
