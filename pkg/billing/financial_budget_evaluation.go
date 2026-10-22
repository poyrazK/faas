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
	out := financial.BudgetEvaluation{PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd, AsOf: report.AsOf, EvaluatedAt: now.UTC(), EvidenceThroughID: report.EvidenceThroughID, CoverageComplete: true, Fresh: true, Reasons: []string{}}
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
			out.Fresh = out.Fresh && meter.Coverage.Fresh
			for _, reason := range meter.Coverage.Reasons {
				out.Reasons = append(out.Reasons, name+":"+reason)
			}
			if !financialBudgetAttributionComplete(spec.Scope, meter.Accrued) {
				out.CoverageComplete = false
				out.Reasons = append(out.Reasons, name+":missing_scope_attribution")
			}
		}
		if !found {
			out.CoverageComplete, out.Fresh = false, false
			out.Reasons = append(out.Reasons, name+":missing_meter")
		}
	}
	amount, err := financial.BudgetAmount(spec, costs)
	if err != nil {
		return out, err
	}
	out.KnownMillicents = &amount
	// A projection from partial or stale evidence would understate the run
	// rate, so only complete, fresh months are projected.
	if out.CoverageComplete && out.Fresh {
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
