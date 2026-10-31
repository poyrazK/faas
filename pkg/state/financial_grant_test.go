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
	half := month.Add(15 * 24 * time.Hour)
	hobby := []state.FinancialPriceSnapshot{{Plan: api.PlanHobby, EffectiveFrom: month,
		Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(api.PlanHobby)}}}
	// Half the month on Hobby (200), half on Pro (1000): 600 GB-h included
	// for the hour the upgrade happened in.
	usage := []state.HourMBSeconds{{Hour: half, MBSeconds: 610*api.SecondsPerGBHour + api.SecondsPerGBHour/2}}
	if got := state.MonthOverageCents(api.PlanPro, hobby, half.Add(time.Minute), usage); got != 10 {
		t.Fatalf("overage = %d cents, want 10 (610.5 GB-h over 600)", got)
	}
	if got := state.MonthOverageCents(api.PlanHobby, hobby, half.Add(time.Minute), usage); got != 410 {
		t.Fatalf("overage = %d cents, want 410 (610.5 GB-h over Hobby's 200)", got)
	}
}

// adr: 20261031090658
func TestBilledComputeOverageFollowsTheAllowanceOfEachHour(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	gbh := api.SecondsPerGBHour
	grant := func(plan api.Plan, at time.Time) state.FinancialPriceSnapshot {
		return state.FinancialPriceSnapshot{Plan: plan, EffectiveFrom: at,
			Price: financial.Price{Meter: "compute", IncludedQuantity: api.IncludedMBSeconds(plan)}}
	}
	hour := func(at time.Time, gbHours int64) state.HourMBSeconds {
		return state.HourMBSeconds{Hour: at, MBSeconds: gbHours * gbh}
	}
	for _, tc := range []struct {
		name      string
		current   api.Plan
		snapshots []state.FinancialPriceSnapshot
		usage     []state.HourMBSeconds
		want      int64 // GB-h
	}{
		{"one plan all month bills usage above its allowance", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month)},
			[]state.HourMBSeconds{hour(month.Add(day), 150), hour(month.Add(20*day), 100)}, 50},
		// Pro until the 16th, then Hobby: 1000/2 + 200/2 = 600 GB-h from the
		// downgrade on. The 700 GB-h used under Pro stay covered; only the
		// 50 GB-h after it bill. Above the month's latest allowance it would
		// read 150 GB-h, 100 of which the provider never invoices.
		{"a downgrade never bills usage the larger allowance covered", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanPro, month), grant(api.PlanHobby, month.Add(15*day))},
			[]state.HourMBSeconds{hour(month.Add(day), 700), hour(month.Add(16*day), 50)}, 50},
		// Hobby until the 16th, then Pro: the 100 GB-h billed under Hobby
		// stay billed although the month's allowance grows to 600.
		{"an upgrade keeps overage already billed", api.PlanPro,
			[]state.FinancialPriceSnapshot{grant(api.PlanHobby, month), grant(api.PlanPro, month.Add(15*day))},
			[]state.HourMBSeconds{hour(month.Add(day), 300), hour(month.Add(16*day), 10)}, 100},
		{"hours are taken in time order", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanPro, month), grant(api.PlanHobby, month.Add(15*day))},
			[]state.HourMBSeconds{hour(month.Add(16*day), 50), hour(month.Add(day), 700)}, 50},
		{"an activation inside an hour applies to that hour", api.PlanHobby,
			[]state.FinancialPriceSnapshot{grant(api.PlanPro, month), grant(api.PlanHobby, month.Add(15*day+30*time.Minute))},
			[]state.HourMBSeconds{hour(month.Add(day), 700), hour(month.Add(15*day), 50)}, 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := state.BilledComputeOverageMBSeconds(tc.current, tc.snapshots, tc.usage); got != tc.want*gbh {
				t.Fatalf("billed = %.1f GB-h, want %d", float64(got)/float64(gbh), tc.want)
			}
		})
	}
}
