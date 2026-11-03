package appmetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/promql"
)

// errCustomMetricHistoryUnavailable means no Prometheus holds the history.
var errCustomMetricHistoryUnavailable = errors.New("custom metric history: prometheus not configured")

// CustomMetricHistory reads a custom metric's pushed values over a fixed
// window from the gregale_app_custom_metric series apid exports (ADR-745).
// custom_metric release gates judge it (ADR-20261019071734 amendment 1):
// app_custom_metrics keeps only the latest value of each metric, so the
// window's samples live in Prometheus.
type CustomMetricHistory struct {
	fetcher PromQL
}

// NewCustomMetricHistory returns nil when no Prometheus is configured, so a
// caller can leave its gate evidence on the store's latest value instead.
func NewCustomMetricHistory(fetcher PromQL) *CustomMetricHistory {
	if fetcher == nil {
		return nil
	}
	if c, ok := fetcher.(*promql.Client); ok && c == nil {
		return nil
	}
	return &CustomMetricHistory{fetcher: fetcher}
}

// CustomMetricWindow counts the values exported in [since, until) and
// averages them. Each apid replica exports the same series, so max() folds
// them. A window with nothing exported has zero samples.
func (h *CustomMetricHistory) CustomMetricWindow(ctx context.Context, appID, name string, since, until time.Time) (int64, float64, error) {
	if h == nil || h.fetcher == nil {
		return 0, 0, errCustomMetricHistoryUnavailable
	}
	if strings.ContainsAny(appID, "\"\n\\") || strings.ContainsAny(name, "\"\n\\") {
		return 0, 0, fmt.Errorf("custom metric history: invalid series selector")
	}
	seconds := int64(math.Ceil(until.Sub(since).Seconds()))
	if seconds < 1 {
		return 0, 0, nil
	}
	selector := fmt.Sprintf(`gregale_app_custom_metric{app=%q,name=%q}[%ds] @ %d`, appID, name, seconds, until.Unix())
	count, err := h.fetcher.QueryScalar(ctx, fmt.Sprintf(`max(count_over_time(%s)) or vector(0)`, selector))
	if err != nil {
		return 0, 0, fmt.Errorf("custom metric history: count: %w", err)
	}
	if count < 1 {
		return 0, 0, nil
	}
	average, err := h.fetcher.QueryScalar(ctx, fmt.Sprintf(`max(avg_over_time(%s))`, selector))
	if err != nil {
		return 0, 0, fmt.Errorf("custom metric history: average: %w", err)
	}
	return int64(count), SafeFloat(average), nil
}
