// adr: 20261101143527 — the plan an hour is billed on follows the recorded
// activations, as the allowance does.
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMonthPlanAtFollowsRecordedActivations(t *testing.T) {
	month := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	grant := func(plan api.Plan, at time.Time) state.FinancialPriceSnapshot {
		return state.FinancialPriceSnapshot{Plan: plan, EffectiveFrom: at, RecordedAt: at,
			Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(plan)}}
	}
	history := []state.FinancialPriceSnapshot{
		grant(api.PlanHobby, month.Add(20*day)), grant(api.PlanPro, month.Add(2*day)), grant(api.PlanFree, month.Add(10*day)),
		{Plan: api.PlanScale, EffectiveFrom: month, Price: financial.Price{Meter: "egress"}},
	}
	for _, tc := range []struct {
		name    string
		current api.Plan
		history []state.FinancialPriceSnapshot
		through time.Time
		want    api.Plan
	}{
		{"no history keeps the current plan", api.PlanHobby, nil, month.Add(day), api.PlanHobby},
		{"before the first activation the earliest applies", api.PlanHobby, history, month.Add(day), api.PlanPro},
		{"the latest activation before the hour's end applies", api.PlanHobby, history, month.Add(12 * day), api.PlanFree},
		{"an activation inside the hour applies to it", api.PlanHobby, history, month.Add(20*day + time.Minute), api.PlanHobby},
		{"an activation at the hour's end does not", api.PlanHobby, history, month.Add(20 * day), api.PlanFree},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := state.MonthPlanAt(tc.current, tc.history, tc.through); got != tc.want {
				t.Fatalf("MonthPlanAt = %s, want %s", got, tc.want)
			}
		})
	}
}
