package preflight

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The allowance each plan includes, expressed as the running time it actually
// buys. Static analysis cannot see how much memory an app uses, so preflight
// reports what a plan buys rather than guessing which plan the app needs.
func TestPlanBudgets_ConvertAllowanceToRunningTime(t *testing.T) {
	want := map[api.Plan]struct {
		billedMB int
		minutes  int
	}{
		// ADR-1430 plan table (financial model §1 as amended by the October
		// 2026 COGS report): plan RAM + 8 MB billed per running second.
		api.PlanFree:  {billedMB: 264, minutes: 4654},
		api.PlanHobby: {billedMB: 520, minutes: 23630},
		api.PlanPro:   {billedMB: 1032, minutes: 59534},
		api.PlanScale: {billedMB: 2056, minutes: 89649},
	}

	budgets := PlanBudgets()
	if len(budgets) != len(want) {
		t.Fatalf("got %d budgets, want %d", len(budgets), len(want))
	}
	for _, got := range budgets {
		expected, ok := want[got.Plan]
		if !ok {
			t.Fatalf("unexpected plan %q", got.Plan)
		}
		if got.BilledRAMMB != expected.billedMB {
			t.Errorf("%s BilledRAMMB = %d, want %d", got.Plan, got.BilledRAMMB, expected.billedMB)
		}
		if got.IncludedRunningMinutes != expected.minutes {
			t.Errorf("%s IncludedRunningMinutes = %d, want %d", got.Plan, got.IncludedRunningMinutes, expected.minutes)
		}
	}
}

// Prices come from the plan table, never from a literal in this package.
func TestPlanBudgets_PriceMatchesLimitsTable(t *testing.T) {
	for _, got := range PlanBudgets() {
		limits := api.MustLimitsFor(got.Plan)
		if got.PriceMillicents != limits.PriceMillicents {
			t.Errorf("%s PriceMillicents = %d, want %d", got.Plan, got.PriceMillicents, limits.PriceMillicents)
		}
	}
}
