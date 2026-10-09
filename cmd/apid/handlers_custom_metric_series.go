package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// getCustomMetricSeries serves GET /v1/apps/{slug}/custom-metrics/{name}/series
// (ADR-745): the history Prometheus recorded from the custom-metric exporter.
func (s *server) getCustomMetricSeries(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.customMetricHistoryEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "custom_metric_history_unavailable",
			"Custom metric history unavailable", "custom metric history is not enabled for this deployment"))
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	name := r.PathValue("name")
	if problem := api.ValidateCustomMetricName(name); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = api.CustomMetricSeriesDefaultRange
	}
	if _, ok := api.CustomMetricSeriesSteps[rng]; !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid range",
			"range must be one of: "+strings.Join(customMetricSeriesRanges(), ", ")))
		return
	}
	kind := s.customMetricKind(r.Context(), app.ID, name)
	writeJSON(w, http.StatusOK, s.customMetricSeries(r.Context(), app.ID, name, kind, rng, time.Now().UTC()))
}

// customMetricKind looks up a stored metric's kind, defaulting to gauge for
// unknown or deleted names (their history, if any, is shown raw).
func (s *server) customMetricKind(ctx context.Context, appID, name string) string {
	rows, err := s.store.ListCustomMetrics(ctx, appID)
	if err != nil {
		return state.CustomMetricKindGauge
	}
	for _, row := range rows {
		if row.Name == name && row.Kind == state.CustomMetricKindCounter {
			return state.CustomMetricKindCounter
		}
	}
	return state.CustomMetricKindGauge
}

// customMetricSeries reads one metric's history. Counters are returned as a
// per-second rate. Prometheus failures become a degraded Source with no
// points; the dashboard shares it.
func (s *server) customMetricSeries(ctx context.Context, appID, name, kind, rng string, now time.Time) api.CustomMetricSeriesResponse {
	step := api.CustomMetricSeriesSteps[rng]
	out := api.CustomMetricSeriesResponse{AppID: appID, Name: name, Kind: kind, Range: rng, Step: step, Points: []api.CustomMetricSeriesPoint{}}
	if s.promqlClient == nil {
		out.Source, out.Points = appmetrics.SourceDegradedPrefix+"prometheus not configured", nil
		return out
	}
	window := time.Duration(customMetricRangeHours(rng)) * time.Hour
	// Several apid replicas export the same series; max collapses them.
	query := fmt.Sprintf(`max(gregale_app_custom_metric{app=%q,name=%q})`, appID, name)
	if kind == state.CustomMetricKindCounter {
		query = fmt.Sprintf(`max(rate(gregale_app_custom_metric{app=%q,name=%q}[%s]))`, appID, name, api.CustomMetricCounterRateWindow)
	}
	series, err := s.promqlClient.QueryRange(ctx, query,
		fmt.Sprint(now.Add(-window).Unix()), fmt.Sprint(now.Unix()), step)
	if err != nil {
		if s.log != nil {
			s.log.Warn("apid: custom metric series query failed", "err", strings.ReplaceAll(err.Error(), "\n", " "))
		}
		out.Source, out.Points = appmetrics.SourceDegradedPrefix+appmetrics.TelemetryDegradedReason(err), nil
		return out
	}
	for _, sr := range series {
		for _, v := range sr.Values {
			out.Points = append(out.Points, api.CustomMetricSeriesPoint{At: time.Unix(v.Timestamp, 0).UTC(), Value: v.Value})
		}
	}
	out.Source = appmetrics.SourcePrometheus
	return out
}

func customMetricSeriesRanges() []string {
	out := make([]string, 0, len(api.CustomMetricSeriesSteps))
	for rng := range api.CustomMetricSeriesSteps {
		out = append(out, rng)
	}
	sort.Slice(out, func(i, j int) bool {
		return customMetricRangeHours(out[i]) < customMetricRangeHours(out[j])
	})
	return out
}

func customMetricRangeHours(rng string) int {
	var n int
	_, _ = fmt.Sscanf(rng[:len(rng)-1], "%d", &n)
	if strings.HasSuffix(rng, "d") {
		return n * 24
	}
	return n
}
