package alerts

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// observeSyntheticCheck evaluates the ADR-748 check metrics from meterd's
// run history. Consecutive failures count failed runs back to the most
// recent success, so "gte 2" ignores a single blip; latency is the p95 of
// successful runs in the rule window. A check with no runs in scope has no
// value, so the rule goes unknown rather than reading as healthy. So does a
// paused check, and a check whose newest run is older than
// SyntheticCheckStaleIntervals intervals: old failures must not keep paging
// (ADR-20261031144636).
func (e *Evaluator) observeSyntheticCheck(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	runs, ok := e.store.(state.SyntheticRunStore)
	if !ok {
		return 0, false, skipDegraded
	}
	check, skip := e.liveSyntheticCheck(ctx, rule)
	if skip != "" {
		return 0, false, skip
	}
	var observed float64
	switch rule.Metric {
	case state.AlertMetricSyntheticConsecutiveFailures:
		recent, err := runs.ListSyntheticCheckRuns(ctx, rule.SyntheticCheckID, api.SyntheticCheckRecentRuns)
		if err != nil {
			e.log.Warn("alerts: read synthetic check runs", "rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		stale := time.Duration(api.SyntheticCheckStaleIntervals*check.IntervalSeconds) * time.Second
		if len(recent) == 0 || e.now().Sub(recent[0].StartedAt) > stale {
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

// liveSyntheticCheck reads the rule's check; a paused or missing check has no
// value to alert on.
func (e *Evaluator) liveSyntheticCheck(ctx context.Context, rule state.AlertRule) (state.SyntheticCheck, string) {
	checks, ok := e.store.(state.SyntheticCheckStore)
	if !ok {
		return state.SyntheticCheck{}, skipDegraded
	}
	check, err := checks.GetSyntheticCheck(ctx, rule.AppID, rule.SyntheticCheckID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		return state.SyntheticCheck{}, skipInsufficient
	case err != nil:
		e.log.Warn("alerts: read synthetic check", "rule", rule.ID, "err", err)
		return state.SyntheticCheck{}, skipDegraded
	case !check.Enabled:
		return state.SyntheticCheck{}, skipInsufficient
	}
	return check, ""
}
