package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// EnvironmentAlertMetrics are the request metrics a stage's alert rule can
// read: the gateway counts them per environment (gateway_environment_*,
// labelled by scope; ADR-744 amendment 2026-11-23, ADR-20261104120000
// amendment 2026-11-22).
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
// stage served (scope is the stage's slug), as FetchAlertMetric does over
// production.
func FetchEnvironmentAlertMetric(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, scope, rng, metric string) (float64, string) {
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
	if productionScope(scope) || !api.ValidProjectEnvironmentSlug(scope) {
		return 0, SourceDegradedPrefix + "invalid environment"
	}
	if !IsValidRange(rng) {
		return 0, SourceDegradedPrefix + "invalid range"
	}
	query, normalize, ok := environmentAlertQuery(appID, scope, rng, metric)
	if !ok {
		return 0, SourceDegradedPrefix + "unsupported environment alert metric"
	}
	value, err := fetcher.QueryScalar(ctx, query)
	if err != nil {
		msg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", ""), "\n", "")
		log.Warn("appmetrics: environment alert metric query failed", "metric", metric, "app_id", appID, "scope", scope, "err", msg)
		return 0, SourceDegradedPrefix + TelemetryDegradedReason(err)
	}
	return normalize(value), SourcePrometheus
}

// productionScope reports whether the gateway labels production's requests
// with scope: the named production environment or a legacy app's default.
func productionScope(scope string) bool {
	return scope == "production" || scope == api.DefaultEnvScope
}

// stageSeries selects every stage's per-environment series of an app: every
// scope but production's.
func stageSeries(appID string) requestSeries {
	return requestSeries{
		duration: "gateway_environment_request_duration_seconds", capacityRefusals: "gateway_environment_capacity_refusals_total",
		labels: fmt.Sprintf(`app=%q,scope!~"production|%s"`, appID, api.DefaultEnvScope),
	}
}

func countNormalize(v float64) float64 { return float64(int64(SafeRoundNonNeg(v))) }

var latencyQuantiles = map[string]float64{"latency_p50_ms": .50, "latency_p95_ms": .95, "latency_p99_ms": .99}

func environmentAlertQuery(appID, scope, rng, metric string) (string, func(float64) float64, bool) {
	series := requestSeriesForScope(appID, scope)
	switch metric {
	case "request_count":
		return fmt.Sprintf(`sum(increase(%s[%s])) or vector(0)`, series.counter(""), rng), countNormalize, true
	case "error_rate_pct":
		return series.errorRate(rng), SafePercent, true
	case "latency_p50_ms", "latency_p95_ms", "latency_p99_ms":
		return series.latency(rng, latencyQuantiles[metric]), SafeFloat, true
	case "cold_start_pct":
		return fmt.Sprintf(`(%s) or vector(0)`, PercentRatioQuery(
			fmt.Sprintf(`sum(rate(gateway_environment_cold_boot_total{%s}[%s]))`, series.labels, rng),
			fmt.Sprintf(`sum(rate(%s[%s]))`, series.counter(""), rng))), SafePercent, true
	}
	return "", nil, false
}

// productionAlertQuery is a production rule's request metric: the app's
// requests minus those its stages served, so a stage's traffic fires only
// the stage's rules (ADR-20261104120000). It returns false for a metric the
// request series do not back.
func productionAlertQuery(appID, rng, metric string) (string, func(float64) float64, bool) {
	all, stages := requestSeriesForScope(appID, ""), stageSeries(appID)
	// minus subtracts the stages' scalar, which is absent when no stage
	// served a request.
	minus := func(a, b string) string { return fmt.Sprintf(`(%s - (%s or vector(0)))`, a, b) }
	switch metric {
	case "request_count":
		return fmt.Sprintf(`%s or vector(0)`, minus(
			fmt.Sprintf(`sum(increase(%s[%s]))`, all.counter(""), rng),
			fmt.Sprintf(`sum(increase(%s[%s]))`, stages.counter(""), rng))), countNormalize, true
	case "error_rate_pct":
		failed := minus(all.requestsWithRefusals("rate", rng, failedRequestClass), stages.requestsWithRefusals("rate", rng, failedRequestClass))
		eligible := minus(all.requestsWithRefusals("rate", rng, eligibleRequestClasses), stages.requestsWithRefusals("rate", rng, eligibleRequestClasses))
		return fmt.Sprintf(`(%s) or vector(0)`, PercentRatioQuery(failed, eligible)), SafePercent, true
	case "latency_p50_ms", "latency_p95_ms", "latency_p99_ms":
		allBuckets := fmt.Sprintf(`sum by (le) (rate(%s_bucket{%s%s}[%s]))`, all.duration, all.labels, successfulRequestClass, rng)
		stageBuckets := fmt.Sprintf(`sum by (le) (rate(%s_bucket{%s%s}[%s]))`, stages.duration, stages.labels, successfulRequestClass, rng)
		// Buckets the stages never reached subtract zero.
		buckets := fmt.Sprintf(`(%s - (%s or (%s * 0)))`, allBuckets, stageBuckets, allBuckets)
		return HistogramQuantileMSQuery(latencyQuantiles[metric], buckets, minus(
			fmt.Sprintf(`sum(rate(%s[%s]))`, all.counter(successfulRequestClass), rng),
			fmt.Sprintf(`sum(rate(%s[%s]))`, stages.counter(successfulRequestClass), rng))), SafeFloat, true
	case "cold_start_pct":
		return fmt.Sprintf(`(%s) or vector(0)`, PercentRatioQuery(
			minus(fmt.Sprintf(`sum(rate(gateway_cold_boot_total{%s}[%s]))`, all.labels, rng),
				fmt.Sprintf(`sum(rate(gateway_environment_cold_boot_total{%s}[%s]))`, stages.labels, rng)),
			minus(fmt.Sprintf(`sum(rate(%s[%s]))`, all.counter(""), rng),
				fmt.Sprintf(`sum(rate(%s[%s]))`, stages.counter(""), rng)))), SafePercent, true
	}
	return "", nil, false
}

// appWideAnomalyQuery is the whole app's request metric, stages included,
// which the anomaly baseline's history and sample counts are read over.
func appWideAnomalyQuery(appID, rng, metric string) (string, func(float64) float64) {
	all := requestSeriesForScope(appID, "")
	switch metric {
	case "request_count":
		return fmt.Sprintf(`sum(increase(%s[%s])) or vector(0)`, all.counter(""), rng), countNormalize
	case "error_rate_pct":
		return all.errorRate(rng), SafePercent
	case "latency_p50_ms", "latency_p95_ms", "latency_p99_ms":
		return all.latency(rng, latencyQuantiles[metric]), SafeFloat
	}
	return alertMetricQuery(appID, rng, metric)
}
