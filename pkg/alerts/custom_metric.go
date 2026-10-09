package alerts

import (
	"context"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// customMetricLister is the read the evaluator needs to learn a pushed
// metric's kind; both PgStore and MemStore implement it.
type customMetricLister interface {
	ListCustomMetrics(ctx context.Context, appID string) ([]state.CustomMetric, error)
}

// observeCustomMetric evaluates an ADR-745 custom_metric rule. The metric's
// kind decides whether the window is averaged (gauge) or turned into a rate
// (counter). A metric that no longer exists — deleted, or never pushed — has
// no value to compare, so the rule goes unknown instead of firing.
func (e *Evaluator) observeCustomMetric(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	lister, ok := e.store.(customMetricLister)
	if !ok {
		return 0, false, skipDegraded
	}
	rows, err := lister.ListCustomMetrics(ctx, rule.AppID)
	if err != nil {
		e.log.Warn("alerts: list custom metrics", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	kind := ""
	for _, row := range rows {
		if row.Name == rule.CustomMetricName {
			kind = row.Kind
			break
		}
	}
	if kind == "" {
		return 0, false, skipInsufficient
	}
	observed, source := appmetrics.FetchCustomMetricAlert(ctx, e.promQL, e.log, rule.AppID, rule.CustomMetricName, kind, string(rule.WindowSpec))
	switch {
	case appmetrics.IsInsufficientSource(source):
		return 0, false, skipInsufficient
	case source != appmetrics.SourcePrometheus:
		return 0, false, skipDegraded
	}
	return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
}
