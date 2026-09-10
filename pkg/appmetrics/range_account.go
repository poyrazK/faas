// range_account.go — Issue #696 / ADR-082 dashboard follow-up PR:
// the account-wide (fleet-wide) sparkline series for the per-account
// dashboard SLO card.
//
// `FetchRangeAccount` is the per-account twin of FetchRange
// (range.go). The PromQL queries drop the `app=…` label filter so
// the time series spans every app in the requester's account — but
// the PromQL rollup is in fact FLEET-wide (Prometheus has no
// per-account label on these metrics; the per-account view is
// brokered by the dashboard's gate, not the metric layer). The
// per-account view on the same physical series is the same shape
// the existing /v1/apps/metrics endpoint and the account-scoped
// Metrics card use — issue #393 / ADR-042.
//
// The fetch path mirrors range.go so the only difference is the
// PromQL strings and the absence of the appID label-injection
// guard (accountID is the path-param/scope, not a PromQL label).
package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
)

// FetchRangeAccount runs the fleet-wide PromQL pipeline for the
// account-wide dashboard sparkline. Returns the projected series
// on success; on error (or nil fetcher) returns an empty
// RangeSeries — the caller renders the empty-state badge and the
// "degraded:" Source comes from the scalar account SLO fetch
// (cmd/apid/handlers_slo.go::fetchAccountSLO).
//
// The latency percentiles are fleet-wide (no `app=` filter) and
// 2xx-class only, matching the scalar fetchAccountSLO. Error rate
// / cold-boot rate are also fleet-wide ratios.
//
// The bucket count is the same FetchRange uses (60 for 1h, 96
// for 24h at 15m step, 168 for 7d) — see range.go::stepForWindow.
func FetchRangeAccount(ctx context.Context, fetcher RangeFetcher, log *slog.Logger, window string) RangeSeries {
	if log == nil {
		log = slog.Default()
	}
	var out RangeSeries
	if fetcher == nil {
		return out
	}
	step := stepForWindow(window)
	out.Step = step
	start, end := rangeStartEnd(window)
	startStr := formatEpoch(start)
	endStr := formatEpoch(end)

	// Latency percentiles (fleet-wide, 2xx class).
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
			`histogram_quantile(%g, sum by (le)(rate(gateway_request_duration_seconds_bucket{class="2xx"}[%s]))) * 1000`,
			p.q, window)
		rows, err := fetcher.QueryRange(ctx, q, startStr, endStr, step)
		if err != nil || len(rows) == 0 {
			// Drop the err value from the log line. CodeQL's
			// go/clear-text-logging rule treats err passed
			// directly to slog as a tainted-source sink; err.Error()
			// IS a recognised barrier but slog's any→String conversion
			// isn't visible to CodeQL's dataflow on multi-argument
			// calls. The label carries the per-metric disambiguation
			// operators need; the Prometheus error message itself is
			// either "connection refused" / "timeout" (not useful
			// beyond what label conveys) or an upstream message that
			// would need its own triage channel.
			log.Warn("appmetrics: account range latency query failed", "label", p.label)
			continue
		}
		*p.dest = seriesToPoints(rows[0].Values)
	}

	// Error rate (fleet-wide).
	errQ := PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_requests_total{code=~"[45].."}[%s]))`, window),
		fmt.Sprintf(`sum(rate(gateway_requests_total[%s]))`, window))
	if rows, err := fetcher.QueryRange(ctx, errQ, startStr, endStr, step); err == nil && len(rows) > 0 {
		out.ErrorRate = seriesToPoints(rows[0].Values)
	} else {
		// Drop the err value — see the latency-query comment
		// above for the CodeQL reasoning. The Prometheus error
		// message isn't useful beyond what the metric name and
		// Prometheus-client error category already convey; if
		// future triage needs it, route through errHash() or
		// similar (see pkg/logsanitize.HashShort for the pattern).
		log.Warn("appmetrics: account range error_rate query failed")
	}

	// Cold-boot rate (fleet-wide).
	coldQ := PercentRatioQuery(
		fmt.Sprintf(`sum(rate(gateway_cold_boot_total[%s]))`, window),
		fmt.Sprintf(`sum(rate(gateway_requests_total[%s]))`, window))
	if rows, err := fetcher.QueryRange(ctx, coldQ, startStr, endStr, step); err == nil && len(rows) > 0 {
		out.ColdBootRate = seriesToPoints(rows[0].Values)
	} else {
		// Drop the err value — see the latency-query comment above.
		log.Warn("appmetrics: account range cold_boot query failed")
	}

	return out
}
