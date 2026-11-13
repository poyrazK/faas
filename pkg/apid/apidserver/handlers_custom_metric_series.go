package apidserver

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	// labels selects one series by the key the list endpoint returns
	// (ADR-20261108160000 slice C).
	labels := r.URL.Query().Get("labels")
	if len(labels) > 1024 || !utf8.ValidString(labels) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid labels",
			"labels must be a series key as listed for the metric, at most 1024 bytes"))
		return
	}
	kind := s.customMetricKind(r.Context(), app.ID, name)
	writeJSON(w, http.StatusOK, s.customMetricSeries(r.Context(), app.ID, name, labels, kind, rng, time.Now().UTC()))
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
func (s *server) customMetricSeries(ctx context.Context, appID, name, labels, kind, rng string, now time.Time) api.CustomMetricSeriesResponse {
	step := api.CustomMetricSeriesSteps[rng]
	out := api.CustomMetricSeriesResponse{AppID: appID, Name: name, Labels: labels, Kind: kind, Range: rng, Step: step, Points: []api.CustomMetricSeriesPoint{}}
	if s.promqlClient == nil {
		out.Source, out.Points = appmetrics.SourceDegradedPrefix+"prometheus not configured", nil
		return out
	}
	window := time.Duration(customMetricRangeHours(rng)) * time.Hour
	// Several apid replicas export the same series; max collapses them.
	// With labels (ADR-20261108160000 slice C) the history is one series'.
	metric, selector := "gregale_app_custom_metric", fmt.Sprintf(`app=%q,name=%q`, appID, name)
	if labels != "" {
		metric, selector = "gregale_app_custom_metric_series", selector+fmt.Sprintf(`,labels=%q`, labels)
	}
	query := fmt.Sprintf(`max(%s{%s})`, metric, selector)
	if kind == state.CustomMetricKindCounter {
		query = fmt.Sprintf(`max(rate(%s{%s}[%s]))`, metric, selector, api.CustomMetricCounterRateWindow)
	}
	series, err := s.promqlClient.QueryRange(ctx, query,
		fmt.Sprint(now.Add(-window).Unix()), fmt.Sprint(now.Unix()), step)
	if err != nil {
		if s.log != nil {
			s.log.Warn("apid: custom metric series query failed", "err", strings.ReplaceAll(err.Error(), "\n", " "))
		}
		out.Source, out.Points = appmetrics.SourceDegradedPrefix+appmetrics.TelemetryDegradedReason(err), []api.CustomMetricSeriesPoint{}
		return out
	}
	for _, sr := range series {
		for _, v := range sr.Values {
			if math.IsNaN(v.Value) || math.IsInf(v.Value, 0) {
				continue
			}
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
