// spec: §10, ADR-1162, ADR-20261030093512 — the month's allowance follows its plan history.
package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// span is one plan held for part of a month, in whole seconds.
type span struct {
	plan api.Plan
	d    time.Duration
}

// weighted is each plan's allowance for its share of the month.
func weighted(month time.Duration, spans ...span) int64 {
	var sum int64
	for _, s := range spans {
		sum += api.IncludedMBSeconds(s.plan) * int64(s.d/time.Second)
	}
	return sum / int64(month/time.Second)
}

func TestMonthComputeIncludedMBSecondsWeighsPlanHistory(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	length := month.AddDate(0, 1, 0).Sub(month)
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
		want      int64
	}{
		{"no recorded history uses the current plan", api.PlanPro, nil, month.Add(day), api.IncludedMBSeconds(api.PlanPro)},
		{"egress grants are ignored", api.PlanHobby, []state.FinancialPriceSnapshot{egress}, month.Add(day), api.IncludedMBSeconds(api.PlanHobby)},
		{"one plan all month grants its whole allowance", api.PlanScale,
			[]state.FinancialPriceSnapshot{grant(api.PlanScale, month.Add(time.Minute))}, month.Add(29 * day), api.IncludedMBSeconds(api.PlanScale)},
		{"a downgrade keeps the larger plan's share", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanPro, month), grant(api.PlanHobby, month.Add(10*day))}, month.Add(20 * day),
			weighted(length, span{api.PlanPro, 10 * day}, span{api.PlanHobby, length - 10*day})},
		{"an upgrade applies only after its activation", api.PlanPro,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month), grant(api.PlanPro, month.Add(10*day))}, month.Add(5 * day), api.IncludedMBSeconds(api.PlanHobby)},
		{"an upgrade counts from its activation to the month's end", api.PlanPro,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month), grant(api.PlanPro, month.Add(10*day))}, month.Add(11 * day),
			weighted(length, span{api.PlanHobby, 10 * day}, span{api.PlanPro, length - 10*day})},
		// The audit's exploit: five minutes on Scale used to grant Scale's
		// whole allowance for the month.
		{"a brief upgrade grants only its minutes", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month), grant(api.PlanScale, month.Add(10*day)), grant(api.PlanHobby, month.Add(10*day+5*time.Minute))}, month.Add(20 * day),
			weighted(length, span{api.PlanHobby, length - 5*time.Minute}, span{api.PlanScale, 5 * time.Minute})},
		// Subscribing near the month's end no longer grants that month's
		// whole allowance on top of the next month's.
		{"a subscription late in the month grants its days", api.PlanScale,
			[]state.FinancialPriceSnapshot{grant(api.PlanFree, month), grant(api.PlanScale, month.Add(28*day))}, month.Add(29 * day),
			weighted(length, span{api.PlanFree, 28 * day}, span{api.PlanScale, length - 28*day})},
		{"usage before the first activation uses the earliest", api.PlanScale,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month.Add(3*day)), grant(api.PlanScale, month.Add(9*day))}, month.Add(day), api.IncludedMBSeconds(api.PlanHobby)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := state.MonthComputeIncludedMBSeconds(tc.current, tc.snapshots, tc.through); got != tc.want {
				t.Fatalf("included = %d mb_seconds (%.1f GB-h), want %d (%.1f GB-h)", got,
					float64(got)/float64(api.SecondsPerGBHour), tc.want, float64(tc.want)/float64(api.SecondsPerGBHour))
			}
		})
	}
}

func TestMonthOverageCentsCountsAnUncapturedPlanChangeFromNow(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	half := 15 * 24 * time.Hour
	hobby := []state.FinancialPriceSnapshot{{Plan: api.PlanHobby, EffectiveFrom: month,
		Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(api.PlanHobby)}}}
	// 300 GB-h on Hobby before the change, 610.5 GB-h in all. Hours before
	// the change are billed above Hobby's 200: 100. After it, half the month
	// on Hobby (200) and half on Pro (1000) include 600 GB-h: 10.5 more
	// (ADR-20261031100000).
	usage := 610*api.SecondsPerGBHour + api.SecondsPerGBHour/2
	beforeChange := func(time.Time) (int64, error) { return 300 * api.SecondsPerGBHour, nil }
	if got, err := state.MonthOverageCents(api.PlanPro, hobby, month.Add(half), usage, beforeChange); err != nil || got != 110 {
		t.Fatalf("overage = %d cents (%v), want 110 (100 on Hobby + 10.5 over 600)", got, err)
	}
	if got, err := state.MonthOverageCents(api.PlanHobby, hobby, month.Add(half), usage, beforeChange); err != nil || got != 410 {
		t.Fatalf("overage = %d cents (%v), want 410 (610.5 GB-h over Hobby's 200)", got, err)
	}
}
