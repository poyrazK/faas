// range.go — Issue #696 / ADR-082 dashboard follow-up PR: the
// time-bucketed sparkline series for the per-app SLO card.
//
// The customer-facing SLO wire surface (PR #715) returned a single
// scalar per field (p50/p95/p99 latency, error rate, cold-boot
// rate) over the closed-set window vocabulary (1h | 24h | 7d).
// The dashboard card needs the TIME SERIES behind those scalars
// so the latency triple + error rate + cold-boot rate each render
// with a sparkline of their own.
//
// `FetchRange` runs one Prometheus query_range per metric and
// returns the parsed sample series as a flat slice ready for the
// SVG renderer. The 3s per-query budget is the same envelope the
// scalar Fetch uses; the dashboard's handler composes the call
// with a 3s context so a slow Prometheus query degrades to an
// empty slice (and the template renders the empty-state badge
// the same shape the existing Metrics card uses).
//
// Empty result → empty slice (no error). A failed query logs
// and returns an empty slice so a single failed metric does
// not blank the whole card — the existing dashboard Metrics
// card has the same degraded-branch semantics.
//
// Latency is returned as a struct with three sub-series (one per
// percentile) — the dashboard renders a 3-line sparkline, not
// a single flattened series. Error rate / cold-boot rate are
// single series.
//
// Bucket count: 60 for 1h (1m step), 96 for 24h (15m step),
// 168 for 7d (1h step). Prometheus retention is 15d so the
// 7d query is the binding budget.
package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
)

// RangeFetcher is the minimum the range helpers need from
// pkg/promql.Client. Mirrors the PromQL scalar Fetch uses so
// callers can pass the same concrete client. The QueryRange
// method is the only addition.
type RangeFetcher interface {
	QueryRange(ctx context.Context, query, start, end, step string) ([]struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}, error)
}

// SparklinePoint is one (time, value) pair after Prometheus's
// time-bucketed series is projected onto the dashboard's
// rendering surface. Time is UTC; Value is the raw metric
// value (latency in ms, percentage in [0,100], count as int).
// The renderer is the only consumer; it scales the value to
// the SVG viewport so the on-the-wire unit doesn't matter to
// the template.
type SparklinePoint struct {
	Time  time.Time
	Value float64
}

// LatencySeries is the triple of latency percentile series
// the dashboard renders as a 3-line sparkline. Each sub-slice
// is the per-bucket value; absent percentile → empty slice
// (the renderer drops the absent line and labels the gap).
type LatencySeries struct {
	P50 []SparklinePoint
	P95 []SparklinePoint
	P99 []SparklinePoint
}

// RangeSeries is the full set of time-bucketed shapes the
// per-app dashboard card consumes. Latency is the triple;
// ErrorRate / ColdBootRate are single series. Any sub-slice
// may be empty (the metric was missing in the window, or the
// query failed) — the template renders the empty-state badge
// on the absent field.
type RangeSeries struct {
	Latency      LatencySeries
	ErrorRate    []SparklinePoint
	ColdBootRate []SparklinePoint
	// Step is the bucket size the PromQL step param used
	// (e.g. "1m" / "15m" / "1h"). The template uses it to
	// label the x-axis ("5m avg" / "1h avg").
	Step string
}

// stepForWindow returns the Prometheus step param for a
// closed-set SLO window. The bucket count is bounded at 168
// to fit the dashboard's <svg> width without interpolation.
// 1h → 1m (60 buckets), 24h → 15m (96 buckets), 7d → 1h
// (168 buckets). Anything outside the closed set falls back
// to 24h / 15m so the function never panics on a bad caller.
func stepForWindow(window string) string {
	switch window {
	case "1h":
		return "1m"
	case "24h":
		return "15m"
	case "7d":
		return "1h"
	default:
		return "15m"
	}
}

// seriesToPoints projects a Prometheus-bucketed slice onto
// the dashboard's SparklinePoint. Empty input → empty output.
// The Timestamp field is in seconds (Prometheus emits float
// seconds); we convert to nanoseconds then to time.Time so
// the template can render RFC3339 strings without a math op.
func seriesToPoints(values []promql.QueryRangeSample) []SparklinePoint {
	if len(values) == 0 {
		return nil
	}
	out := make([]SparklinePoint, 0, len(values))
	for _, v := range values {
		out = append(out, SparklinePoint{
			Time:  time.Unix(v.Timestamp, 0).UTC(),
			Value: v.Value,
		})
	}
	return out
}

// rangeStartEnd returns the (start, end) anchor pair to feed
// Prometheus's query_range. end is now (UTC) — the dashboard
// always shows the trailing window. start is end - window.
// The bounds match windowToRange in cmd/apid/handlers_slo.go
// so the scalar and the sparkline agree on the time window.
func rangeStartEnd(window string) (time.Time, time.Time) {
	end := time.Now().UTC()
	var start time.Time
	switch window {
	case "1h":
		start = end.Add(-1 * time.Hour)
	case "24h":
		start = end.Add(-24 * time.Hour)
	case "7d":
		start = end.Add(-7 * 24 * time.Hour)
	default:
		start = end.Add(-24 * time.Hour)
	}
	return start, end
}

// formatEpoch formats a time.Time as Prometheus's fractional
// epoch seconds (the format the query_range endpoint expects
// for the start / end params). The default time.Format
// "%.3f" with Float precision is locale-stable enough.
func formatEpoch(t time.Time) string {
	return fmt.Sprintf("%.3f", float64(t.UnixNano())/1e9)
}

// FetchRange runs the per-app PromQL pipeline for the
// dashboard sparkline. Returns the projected series on
// success; on error (or nil fetcher) returns an empty
// RangeSeries — the caller renders the empty-state badge
// and the "degraded:" Source comes from the existing
// scalar fetch the handler does in parallel.
//
// log is the destination for per-query warnings. nil
// falls back to slog.Default() so callers that wire a
// no-log setup don't have to special-case the zero value
// (mirrors scheddgrpc/server.go nil coercion).
//
// The PromQL queries mirror the scalar fetchAppSLO
// (cmd/apid/handlers_slo.go:127) PromQL strings so the
// sparkline and the headline numbers are computed over
// the same data — one of the stickier quality bugs in
// ad-hoc dashboards is showing "p95 = 87ms" but a
// sparkline that ends at 142ms because the two queries
// used different range vectors.
func FetchRange(ctx context.Context, fetcher RangeFetcher, log *slog.Logger, appID, window string) RangeSeries {
	if log == nil {
		log = slog.Default()
	}
	step := stepForWindow(window)
	var out RangeSeries
	out.Step = step
	// Nil-client short-circuit. The same posture as the
	// scalar Fetch — the caller wraps the result into a
	// "degraded:" Source from the scalar fetch. Step is
	// populated even on the nil-fetcher path so the
	// dashboard template can render the x-axis label
	// ("5m avg" / "1h avg") before the empty-state badge.
	if fetcher == nil {
		return out
	}
	// Label-injection guard. The scalar Fetch has the same
	// guard for the same reason — a crafted appID containing
	// `"` would close the outer label prematurely and let a
	// caller escape their app's data boundary.
	if strings.ContainsAny(appID, "\"\n\\") {
		return out
	}
	start, end := rangeStartEnd(window)
	startStr := formatEpoch(start)
	endStr := formatEpoch(end)

	// Latency percentiles — three query_range calls, each
	// emits one series (sum by (le) collapses the per-app
	// fan-out into a single bucket series).
	for _, p := range []struct {
		q     float64
		dest  *[]SparklinePoint
		label string
	}{
		{q: 0.50, dest: &out.Latency.P50, label: "p50"},
		{q: 0.95, dest: &out.Latency.P95, label: "p95"},
		{q: 0.99, dest: &out.Latency.P99, label: "p99"},
	} {
		q := fmt.Sprintf(
			`histogram_quantile(%g, sum by (le)(rate(gateway_request_duration_seconds_bucket{app=%q,class="2xx"}[%s]))) * 1000`,
			p.q, appID, window)
		rows, err := fetcher.QueryRange(ctx, q, startStr, endStr, step)
		if err != nil || len(rows) == 0 {
			log.Warn("appmetrics: range latency query failed", "label", p.label, "app_id", appID, "err", err)
			continue
		}
		*p.dest = seriesToPoints(rows[0].Values)
	}

	// Error rate — single series. Mirrors the scalar's
	// ratio of [45]xx over all requests, multiplied by 100.
	errQ := PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q,class=~"[45]xx"}[%s]))`, appID, window),
		fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q}[%s]))`, appID, window))
	if rows, err := fetcher.QueryRange(ctx, errQ, startStr, endStr, step); err == nil && len(rows) > 0 {
		out.ErrorRate = seriesToPoints(rows[0].Values)
	} else {
		log.Warn("appmetrics: range error_rate query failed", "app_id", appID, "err", err)
	}

	// Cold-boot rate — single series.
	coldQ := PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_cold_boot_total{app=%q}[%s]))`, appID, window),
		fmt.Sprintf(`sum(rate(gateway_request_duration_seconds_count{app=%q}[%s]))`, appID, window))
	if rows, err := fetcher.QueryRange(ctx, coldQ, startStr, endStr, step); err == nil && len(rows) > 0 {
		out.ColdBootRate = seriesToPoints(rows[0].Values)
	} else {
		log.Warn("appmetrics: range cold_boot query failed", "app_id", appID, "err", err)
	}

	return out
}
