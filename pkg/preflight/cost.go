package preflight

import "github.com/onebox-faas/faas/pkg/api"

// planOrder is the customer-facing ladder, cheapest first.
var planOrder = []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro, api.PlanScale}

// PlanBudgets returns every plan's allowance converted to running time. All
// values derive from pkg/api/limits.go; nothing here is a literal.
func PlanBudgets() []PlanBudget {
	budgets := make([]PlanBudget, 0, len(planOrder))
	for _, plan := range planOrder {
		limits, ok := api.LimitsFor(plan)
		if !ok {
			continue
		}
		billed := api.BillableRAMMB(limits.RAMMB)
		budgets = append(budgets, PlanBudget{
			Plan:        plan,
			RAMMB:       limits.RAMMB,
			BilledRAMMB: billed,
			// Integer arithmetic throughout: minutes = GB-hours × MB-per-GB ×
			// minutes-per-hour ÷ billed MB. No floats near a billing figure.
			IncludedRunningMinutes:     includedRunningMinutes(limits.IncludedGBHours, billed),
			IncludedGBHours:            limits.IncludedGBHours,
			PriceMillicents:            limits.PriceMillicents,
			OverageMillicentsPerGBHour: api.OverageMillicentsPerGBHour,
		})
	}
	return budgets
}

func includedRunningMinutes(includedGBHours, billedMB int) int {
	if billedMB <= 0 {
		return 0
	}
	return includedGBHours * 1024 * 60 / billedMB
}
