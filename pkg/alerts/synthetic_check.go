package alerts

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// observeSyntheticCheck evaluates the ADR-748 check metrics from meterd's
// run history. Consecutive failures count failed runs back to the most
// recent success, so "gte 2" ignores a single blip; latency is the p95 of
// successful runs in the rule window. A check with no runs in scope has no
// value, so the rule goes unknown rather than reading as healthy.
func (e *Evaluator) observeSyntheticCheck(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	runs, ok := e.store.(state.SyntheticRunStore)
	if !ok {
		return 0, false, skipDegraded
	}
	var observed float64
	switch rule.Metric {
	case state.AlertMetricSyntheticConsecutiveFailures:
		recent, err := runs.ListSyntheticCheckRuns(ctx, rule.SyntheticCheckID, api.SyntheticCheckRecentRuns)
		if err != nil {
			e.log.Warn("alerts: read synthetic check runs", "rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		if len(recent) == 0 {
			return 0, false, skipInsufficient
		}
		for _, run := range recent {
			if run.OK {
				break
			}
			observed++
		}
	default:
		st, err := runs.SyntheticCheckStats(ctx, rule.SyntheticCheckID, e.windowStart(rule.WindowSpec, e.now()))
		if err != nil {
			e.log.Warn("alerts: synthetic check stats", "rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		if st.OKRuns == 0 {
			return 0, false, skipInsufficient
		}
		observed = st.P95LatencyMS
	}
	return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
}
