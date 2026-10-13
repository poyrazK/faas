package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/promql"
)

// Custom metric kinds as stored in app_custom_metrics.kind (ADR-745).
const (
	CustomMetricKindGauge   = "gauge"
	CustomMetricKindCounter = "counter"
)

// FetchCustomMetricAlert evaluates one ADR-745 custom_metric alert rule
// against the gregale_app_custom_metric series apid exports. A gauge is
// judged on its average over the rule window, so a 5m window tracks the
// current value and a 1h window asks whether it stayed there; a counter is
// judged on its per-second rate over the window, because its cumulative
// total only grows. max() folds the identical series each apid replica
// exports.
//
// A window with no exported samples (nothing pushed, or every value went
// stale) is insufficient rather than zero: a stopped pusher must not fire a
// "below" rule or satisfy an "above" rule by silence.
func FetchCustomMetricAlert(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, name, kind, rng string) (float64, string) {
	if log == nil {
		log = slog.Default()
	}
	if fetcher == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if c, ok := fetcher.(*promql.Client); ok && c == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if strings.ContainsAny(appID, "\"\n\\") || strings.ContainsAny(name, "\"\n\\") {
		return 0, SourceDegradedPrefix + "invalid series selector"
	}
	if !IsValidRange(rng) {
		return 0, SourceDegradedPrefix + "invalid range"
	}
	fn := "avg_over_time"
	if kind == CustomMetricKindCounter {
		fn = "rate"
	}
	query := fmt.Sprintf(`max(%s(gregale_app_custom_metric{app=%q,name=%q}[%s])) or vector(-1)`, fn, appID, name, rng)
	value, err := fetcher.QueryScalar(ctx, query)
	if err != nil {
		msg := strings.ReplaceAll(err.Error(), "\r", "")
		msg = strings.ReplaceAll(msg, "\n", "")
		log.Warn("appmetrics: custom metric alert query failed", "app_id", appID, "name", name, "err", msg)
		return 0, SourceDegradedPrefix + TelemetryDegradedReason(err)
	}
	if value < 0 {
		return 0, SourceInsufficientPrefix + "no fresh values pushed in the window"
	}
	return SafeFloat(value), SourcePrometheus
}
