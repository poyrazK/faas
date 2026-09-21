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
package main

import (
	"encoding/json"
	"errors"
	"net/http"
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
	err := s.store.PutCustomMetric(r.Context(), app.ID, name, req.Value, time.Now(), api.MaxCustomMetricsPerApp)
	if errors.Is(err, state.ErrCustomMetricLimit) {
		api.WriteProblem(w, api.ErrCustomMetricLimitReached(api.MaxCustomMetricsPerApp))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		api.WriteProblem(w, api.ErrInternal(err.Error()))
		return
	}
	out := api.CustomMetricListResponse{
		Metrics:    make([]api.CustomMetricResponse, 0, len(rows)),
		FreshnessS: api.CustomMetricFreshnessSeconds,
		MaxMetrics: api.MaxCustomMetricsPerApp,
	}
	now := time.Now()
	freshness := time.Duration(api.CustomMetricFreshnessSeconds) * time.Second
	for _, row := range rows {
		out.Metrics = append(out.Metrics, api.CustomMetricResponse{
			Name:       row.Name,
			Value:      row.Value,
			ObservedAt: row.ObservedAt.UTC(),
			// Stale is computed here rather than left to the client so
			// the API and the scheduler cannot disagree about which rows
			// are driving scaling.
			Stale: now.Sub(row.ObservedAt) > freshness,
		})
	}
	writeJSON(w, http.StatusOK, out)
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
		api.WriteProblem(w, api.ErrInternal(err.Error()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
