package billing

import (
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// EvaluateFinancialBudget reuses the exact historical price/allowance
// allocations used by cost reports. Unknown scope membership stays partial.
func EvaluateFinancialBudget(report api.FinancialCostsResponse, spec financial.BudgetSpec, now time.Time) (financial.BudgetEvaluation, error) {
	out := financial.BudgetEvaluation{PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd, AsOf: report.AsOf, EvaluatedAt: now.UTC(), EvidenceThroughID: report.EvidenceThroughID, CoverageComplete: true, Projectable: true, Fresh: true, Reasons: []string{}}
	var costs []financial.ContractCosts
	for _, name := range spec.Meters {
		found := false
		for _, meter := range report.Meters {
			if meter.Meter != name {
				continue
			}
			found = true
			costs = append(costs, meter.Accrued)
			out.CoverageComplete = out.CoverageComplete && meter.Coverage.Complete
			// A few unsampled minutes still project; unpriced usage does not.
			out.Projectable = out.Projectable && (meter.Coverage.Complete || meter.Coverage.Projectable && meter.Coverage.UnpricedQuantity == 0)
			out.Fresh = out.Fresh && meter.Coverage.Fresh
			for _, reason := range meter.Coverage.Reasons {
				out.Reasons = append(out.Reasons, name+":"+reason)
			}
			if !financialBudgetAttributionComplete(spec.Scope, meter.Accrued) {
				out.CoverageComplete, out.Projectable = false, false
				out.Reasons = append(out.Reasons, name+":missing_scope_attribution")
			}
		}
		if !found {
			out.CoverageComplete, out.Projectable, out.Fresh = false, false, false
			out.Reasons = append(out.Reasons, name+":missing_meter")
		}
	}
	amount, err := financial.BudgetAmount(spec, costs)
	if err != nil {
		return out, err
	}
	out.KnownMillicents = &amount
	// A projection from stale evidence, or from a month missing more than a
	// few sampled minutes, would misstate the run rate; each meter's forecast
	// already extrapolates over its covered minutes only.
	if out.Projectable && out.Fresh {
		if out.ProjectedMillicents, out.ProjectionReason, err = ProjectFinancialBudget(report, spec); err != nil {
			return out, err
		}
	} else {
		out.ProjectionReason = "incomplete_evidence"
	}
	slices.Sort(out.Reasons)
	out.Reasons = slices.Compact(out.Reasons)
	return out, nil
}
