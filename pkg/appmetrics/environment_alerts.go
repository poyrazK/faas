package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/promql"
)

// EnvironmentAlertMetrics are the request metrics a stage's alert rule can
// read: the gateway counts them per environment host
// (gateway_environment_*; ADR-20261104120000 amendment 2026-11-22).
var EnvironmentAlertMetrics = []string{
	"request_count", "error_rate_pct", "latency_p50_ms", "latency_p95_ms", "latency_p99_ms", "cold_start_pct",
}

// IsEnvironmentAlertMetric reports whether metric is in EnvironmentAlertMetrics.
func IsEnvironmentAlertMetric(metric string) bool {
	for _, m := range EnvironmentAlertMetrics {
		if m == metric {
			return true
		}
	}
	return false
}

// FetchEnvironmentAlertMetric evaluates an alert metric over the requests one
// environment host served, as FetchAlertMetric does over the whole app.
func FetchEnvironmentAlertMetric(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, environmentID, rng, metric string) (float64, string) {
	if log == nil {
		log = slog.Default()
	}
	if fetcher == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if c, ok := fetcher.(*promql.Client); ok && c == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if strings.ContainsAny(appID, "\"\n\\") || appID == "" {
		return 0, SourceDegradedPrefix + "invalid app id"
	}
	if strings.ContainsAny(environmentID, "\"\n\\") || environmentID == "" {
		return 0, SourceDegradedPrefix + "invalid environment id"
	}
	if !IsValidRange(rng) {
		return 0, SourceDegradedPrefix + "invalid range"
	}
	query, normalize, ok := environmentAlertQuery(appID, environmentID, rng, metric)
	if !ok {
		return 0, SourceDegradedPrefix + "unsupported environment alert metric"
	}
	value, err := fetcher.QueryScalar(ctx, query)
	if err != nil {
		msg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", ""), "\n", "")
		log.Warn("appmetrics: environment alert metric query failed", "metric", metric, "app_id", appID, "environment_id", environmentID, "err", msg)
		return 0, SourceDegradedPrefix + TelemetryDegradedReason(err)
	}
	return normalize(value), SourcePrometheus
}

func environmentAlertQuery(appID, environmentID, rng, metric string) (string, func(float64) float64, bool) {
	sel := fmt.Sprintf(`app=%q,environment=%q`, appID, environmentID)
	count := func(v float64) float64 { return float64(int64(SafeRoundNonNeg(v))) }
	switch metric {
	case "request_count":
		return fmt.Sprintf(`sum(increase(gateway_environment_request_duration_seconds_count{%s}[%s])) or vector(0)`, sel, rng), count, true
	case "error_rate_pct":
		// As ErrorRatePctQuery: 5xx and capacity refusals over 2xx, 5xx and
		// capacity refusals.
		failed := fmt.Sprintf(`sum(rate(gateway_environment_request_duration_seconds_count{%[1]s,class="5xx"}[%[2]s]) or rate(gateway_environment_capacity_refusals_total{%[1]s}[%[2]s]))`, sel, rng)
		eligible := fmt.Sprintf(`sum(rate(gateway_environment_request_duration_seconds_count{%[1]s,class=~"2xx|5xx"}[%[2]s]) or rate(gateway_environment_capacity_refusals_total{%[1]s}[%[2]s]))`, sel, rng)
		return fmt.Sprintf(`(%s) or vector(0)`, PercentRatioQuery(failed, eligible)), SafePercent, true
	case "latency_p50_ms", "latency_p95_ms", "latency_p99_ms":
		quantile := map[string]float64{"latency_p50_ms": .50, "latency_p95_ms": .95, "latency_p99_ms": .99}[metric]
		return HistogramQuantileMSQuery(quantile,
			fmt.Sprintf(`sum by (le) (rate(gateway_environment_request_duration_seconds_bucket{%s,class="2xx"}[%s]))`, sel, rng),
			fmt.Sprintf(`sum(rate(gateway_environment_request_duration_seconds_count{%s,class="2xx"}[%s]))`, sel, rng)), SafeFloat, true
	case "cold_start_pct":
		return fmt.Sprintf(`(%s) or vector(0)`, PercentRatioQuery(
			fmt.Sprintf(`sum(rate(gateway_environment_cold_boot_total{%s}[%s]))`, sel, rng),
			fmt.Sprintf(`sum(rate(gateway_environment_request_duration_seconds_count{%s}[%s]))`, sel, rng))), SafePercent, true
	}
	return "", nil, false
}
