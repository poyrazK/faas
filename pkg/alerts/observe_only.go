package alerts

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Evaluator) evalObserveOnly(ctx context.Context, rule state.AlertRule, now time.Time, stats *Stats) {
	store, ok := e.store.(state.AlertObservationStore)
	if !ok {
		e.log.Warn("alerts: observe-only storage unavailable", "rule", rule.ID)
		return
	}
	e.clearChannelFires(rule.ID)
	octx, cancel := context.WithTimeout(ctx, api.AlertObserveBudget)
	result := e.observeAnomaly(octx, rule)
	cancel()
	if result.fires {
		result.evidence.Reason = api.AlertAnomalyObservedBreach
		result.evidence.BaselineDays = &result.baseline.days
	}
	accepted, err := store.RecordAlertObservation(ctx, rule, now, result.evidence)
	if err != nil {
		e.log.Warn("alerts: record observe-only evaluation", "rule", rule.ID, "err", err)
	}
	if !accepted {
		return
	}
	switch result.skip {
	case skipDegraded:
		stats.SkippedDegraded++
		if e.ops != nil {
			e.ops.AlertEvalSkippedDegradedTotal()()
		}
	case skipInsufficient:
		stats.SkippedInsufficient++
	}
}
