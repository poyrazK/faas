// spec: §10, ADR-1162 — the month's allowance follows its plan history.
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMonthComputeIncludedMBSecondsFollowsPlanHistory(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	grant := func(plan api.Plan, at time.Time) state.FinancialPriceSnapshot {
		return state.FinancialPriceSnapshot{Plan: plan, EffectiveFrom: at,
			Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(plan)}}
	}
	egress := state.FinancialPriceSnapshot{EffectiveFrom: month,
		Price: financial.Price{Meter: "egress", IncludedQuantity: 1 << 40}}
	day := 24 * time.Hour
	for _, tc := range []struct {
		name      string
		current   api.Plan
		snapshots []state.FinancialPriceSnapshot
		through   time.Time
		want      api.Plan
	}{
		{"no recorded history uses the current plan", api.PlanPro, nil, month.Add(day), api.PlanPro},
		{"egress grants are ignored", api.PlanHobby, []state.FinancialPriceSnapshot{egress}, month.Add(day), api.PlanHobby},
		{"downgrade keeps the larger grant", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanPro, month), grant(api.PlanHobby, month.Add(10*day))}, month.Add(20 * day), api.PlanPro},
		{"upgrade applies only after its activation", api.PlanPro,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month), grant(api.PlanPro, month.Add(10*day))}, month.Add(5 * day), api.PlanHobby},
		{"upgrade applies from its activation", api.PlanPro,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month), grant(api.PlanPro, month.Add(10*day))}, month.Add(11 * day), api.PlanPro},
		{"usage before the first activation uses the earliest", api.PlanScale,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month.Add(3*day)), grant(api.PlanScale, month.Add(9*day))}, month.Add(day), api.PlanHobby},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := state.MonthComputeIncludedMBSeconds(tc.current, tc.snapshots, tc.through), api.IncludedMBSeconds(tc.want); got != want {
				t.Fatalf("included = %d, want %d (%s)", got, want, tc.want)
			}
		})
	}
}

func TestMonthOverageCentsNeverUsesLessThanCurrentPlan(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	// The upgrade to Pro is not captured yet: the current plan still counts.
	hobby := []state.FinancialPriceSnapshot{{Plan: api.PlanHobby, EffectiveFrom: month,
		Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(api.PlanHobby)}}}
	usage := 1010 * api.SecondsPerGBHour
	if got := state.MonthOverageCents(api.PlanPro, hobby, month.Add(time.Hour), usage); got != 10 {
		t.Fatalf("overage = %d cents, want 10 (1010 GB-h over Pro's 1000)", got)
	}
	if got := state.MonthOverageCents(api.PlanHobby, hobby, month.Add(time.Hour), usage); got != 810 {
		t.Fatalf("overage = %d cents, want 810 (1010 GB-h over Hobby's 200)", got)
	}
}
