package alerts

// ADR-1192: alerts on edge-rule activity, read from the ADR-960 hourly hit
// buckets. edge_rule_hits counts enforced matches, edge_rule_log_hits
// log-mode matches; an empty EdgeRuleID sums every rule of the app.

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) observeEdgeRuleHits(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	store, ok := e.store.(state.EdgeRuleHitStore)
	if !ok || rule.AppID == "" {
		return 0, false, skipDegraded
	}
	stats, err := store.EdgeRuleHitStatsForApp(ctx, rule.AppID, e.windowStart(rule.WindowSpec, e.now()))
	if err != nil {
		e.log.Warn("alerts: read edge-rule hit counts", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	value := float64(edgeRuleHitTotal(stats, rule.EdgeRuleID, rule.Metric == state.AlertMetricEdgeRuleLogHits))
	return value, compareFloat(value, rule.Comparison, rule.Threshold), ""
}

// edgeRuleHitTotal sums matched (or logged) hits, for one rule when ruleID
// is set. A rule that no longer exists has no buckets and counts zero.
func edgeRuleHitTotal(stats []state.EdgeRuleHitStats, ruleID string, logged bool) int64 {
	var total int64
	for _, st := range stats {
		if ruleID != "" && st.RuleID != ruleID {
			continue
		}
		if logged {
			total += st.Logged
		} else {
			total += st.Matched
		}
	}
	return total
}
