package appmetrics

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/promql"
)

// Custom metric kinds as stored in app_custom_metrics.kind (ADR-745).
const (
	CustomMetricKindGauge   = "gauge"
	CustomMetricKindCounter = "counter"
)

// FetchCustomMetricAlert evaluates one ADR-745 custom_metric alert rule
// against the gregale_app_custom_metric series apid exports, or with a
// series key the one labelled series of gregale_app_custom_metric_series. A gauge is
// judged on its average over the rule window, so a 5m window tracks the
// current value and a 1h window asks whether it stayed there; a counter is
// judged on its per-second rate over the window, because its cumulative
// total only grows. max() folds the identical series each apid replica
// exports.
//
// A window with no exported samples (nothing pushed, or every value went
// stale) is insufficient rather than zero: a stopped pusher must not fire a
// "below" rule or satisfy an "above" rule by silence.
func FetchCustomMetricAlert(ctx context.Context, fetcher PromQL, log *slog.Logger, appID, name, seriesKey, kind, rng string) (float64, string) {
	if log == nil {
		log = slog.Default()
	}
	if fetcher == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	if c, ok := fetcher.(*promql.Client); ok && c == nil {
		return 0, SourceDegradedPrefix + "prometheus not configured"
	}
	series, ok := customMetricSelector(appID, name, seriesKey)
	if !ok {
		return 0, SourceDegradedPrefix + "invalid series selector"
	}
	if !IsValidRange(rng) {
		return 0, SourceDegradedPrefix + "invalid range"
	}
	fn := "avg_over_time"
	if kind == CustomMetricKindCounter {
		fn = "rate"
	}
	query := fmt.Sprintf(`max(%s(%s[%s])) or vector(-1)`, fn, series, rng)
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

// customMetricSelector is the PromQL selector of a custom metric's aggregate,
// or with a non-empty seriesKey of that one labelled series (ADR-20261108160000
// slice C2). App IDs and names are closed shapes, so one carrying a quote,
// backslash or newline is refused; a series key holds arbitrary label values
// and is quoted instead, which PromQL unquotes with Go's rules.
func customMetricSelector(appID, name, seriesKey string) (string, bool) {
	if strings.ContainsAny(appID, "\"\n\\") || strings.ContainsAny(name, "\"\n\\") || !utf8.ValidString(seriesKey) {
		return "", false
	}
	if seriesKey == "" {
		return fmt.Sprintf(`gregale_app_custom_metric{app=%q,name=%q}`, appID, name), true
	}
	return fmt.Sprintf(`gregale_app_custom_metric_series{app=%q,name=%q,labels=%s}`, appID, name, strconv.Quote(seriesKey)), true
}
