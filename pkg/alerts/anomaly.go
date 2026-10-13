package alerts

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// anomalyBaseline is one rule's ADR-744 baseline: the median of the metric
// at the same time of day over the previous days that had traffic.
type anomalyBaseline struct {
	value      float64
	days       int
	computedAt time.Time
}

// anomalyState caches baselines (they move slowly, so one computation per
// rule per AnomalyBaselineCacheTTL) and remembers the baseline behind each
// rule's latest observation for the webhook payload.
type anomalyState struct {
	mu        sync.Mutex
	baselines map[string]anomalyBaseline
}

// anomalyKey invalidates the cache when the rule's app, metric, or window
// changes.
func anomalyKey(rule state.AlertRule) string {
	return rule.ID + "|" + rule.AppID + "|" + string(rule.Metric) + "|" + string(rule.WindowSpec)
}

// observeAnomaly evaluates an above_baseline / below_baseline rule.
func (e *Evaluator) observeAnomaly(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	metric, window := string(rule.Metric), string(rule.WindowSpec)
	observed, source := appmetrics.FetchAlertMetric(ctx, e.promQL, e.log, rule.AppID, window, metric)
	if source != appmetrics.SourcePrometheus {
		return 0, false, skipDegraded
	}
	baseline, ok := e.anomalyBaselineFor(ctx, rule)
	if !ok {
		return 0, false, skipDegraded
	}
	if baseline.days < api.AnomalyBaselineMinDays {
		return observed, false, skipInsufficient
	}
	switch rule.Comparison {
	case state.AlertAboveBaseline:
		if baseline.value <= 0 || observed < api.AnomalyAlertMetrics[metric] {
			return observed, false, ""
		}
		if metric != "request_count" {
			requests, src := appmetrics.FetchAlertMetric(ctx, e.promQL, e.log, rule.AppID, window, "request_count")
			if src != appmetrics.SourcePrometheus {
				return 0, false, skipDegraded
			}
			if requests < api.AnomalyAlertMinRequests {
				return observed, false, ""
			}
		}
		return observed, observed >= baseline.value*rule.Threshold, ""
	case state.AlertBelowBaseline:
		if baseline.value < api.AnomalyAlertMinRequests {
			return observed, false, ""
		}
		return observed, observed <= baseline.value*rule.Threshold, ""
	default:
		return 0, false, skipDegraded
	}
}

// anomalyBaselineFor returns the cached baseline or recomputes it. ok is
// false when Prometheus could not answer, so the rule degrades rather than
// firing on a partial history.
func (e *Evaluator) anomalyBaselineFor(ctx context.Context, rule state.AlertRule) (anomalyBaseline, bool) {
	key, now := anomalyKey(rule), e.now()
	e.anomaly.mu.Lock()
	cached, hit := e.anomaly.baselines[key]
	e.anomaly.mu.Unlock()
	if hit && now.Sub(cached.computedAt) < api.AnomalyBaselineCacheTTL {
		return cached, true
	}
	metric, window := string(rule.Metric), string(rule.WindowSpec)
	var values []float64
	for day := 1; day <= api.AnomalyBaselineDays; day++ {
		requests, src := appmetrics.FetchAlertMetricDaysAgo(ctx, e.promQL, e.log, rule.AppID, window, "request_count", day)
		if src != appmetrics.SourcePrometheus {
			return anomalyBaseline{}, false
		}
		if requests < 1 {
			continue // no traffic that day: not evidence of "usual"
		}
		value := requests
		if metric != "request_count" {
			if value, src = appmetrics.FetchAlertMetricDaysAgo(ctx, e.promQL, e.log, rule.AppID, window, metric, day); src != appmetrics.SourcePrometheus {
				return anomalyBaseline{}, false
			}
		}
		values = append(values, value)
	}
	b := anomalyBaseline{value: median(values), days: len(values), computedAt: now}
	e.anomaly.mu.Lock()
	if e.anomaly.baselines == nil {
		e.anomaly.baselines = map[string]anomalyBaseline{}
	}
	e.anomaly.baselines[key] = b
	e.anomaly.mu.Unlock()
	return b, true
}

// anomalyPayload adds the baseline behind a firing ADR-744 rule to its
// webhook payload so a receiver can say "4.2%, usually 1.1% (3.8x)".
func (e *Evaluator) anomalyPayload(rule state.AlertRule, observed float64) map[string]any {
	if !api.IsAnomalyAlertComparison(string(rule.Comparison)) {
		return nil
	}
	e.anomaly.mu.Lock()
	b, ok := e.anomaly.baselines[anomalyKey(rule)]
	e.anomaly.mu.Unlock()
	if !ok {
		return nil
	}
	extra := map[string]any{"baseline": b.value, "baseline_days": b.days}
	if b.value > 0 {
		extra["ratio"] = observed / b.value
	}
	return extra
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}
