package alerts

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/slo"
	"github.com/onebox-faas/faas/pkg/state"
)

// observeCustomSLO evaluates the ADR-747 SLO metrics. slo_budget_burn is
// the multi-window burn rate against the SLO's own objective (live from
// Prometheus); slo_budget_remaining_pct is the share of the window's budget
// left, from the hourly rows meterd records. An SLO with no traffic in its
// window has no budget position, so that rule goes unknown rather than
// reading as 100% and suppressing a real "below" alert later.
func (e *Evaluator) observeCustomSLO(ctx context.Context, rule state.AlertRule) (float64, bool, string) {
	defs, ok := e.store.(state.SLOStore)
	if !ok {
		return 0, false, skipDegraded
	}
	def, err := defs.GetSLO(ctx, rule.AppID, rule.SLOID)
	if errors.Is(err, state.ErrNotFound) {
		return 0, false, skipInsufficient
	}
	if err != nil {
		e.log.Warn("alerts: read SLO", "rule", rule.ID, "err", err)
		return 0, false, skipDegraded
	}
	var observed float64
	switch rule.Metric {
	case state.AlertMetricSLOBudgetBurn:
		if e.promQL == nil {
			return 0, false, skipDegraded
		}
		observed, err = slo.MultiWindowBurn(ctx, e.promQL, def)
		if err != nil {
			e.log.Warn("alerts: SLO burn rate", "rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
	default:
		budgets, ok := e.store.(state.SLOBudgetStore)
		if !ok {
			return 0, false, skipDegraded
		}
		current := e.now().UTC().Truncate(time.Hour)
		start := current.Add(-time.Duration(def.WindowDays) * 24 * time.Hour)
		if created := def.CreatedAt.UTC().Truncate(time.Hour); created.After(start) {
			start = created
		}
		totals, err := budgets.SumSLOHours(ctx, def.ID, start)
		if err != nil {
			e.log.Warn("alerts: SLO budget", "rule", rule.ID, "err", err)
			return 0, false, skipDegraded
		}
		remaining, ok := slo.BudgetRemaining(totals.Good, totals.Total, def.ObjectiveBP)
		if !ok {
			return 0, false, skipInsufficient
		}
		observed = remaining * 100
	}
	return observed, compareFloat(observed, rule.Comparison, rule.Threshold), ""
}
