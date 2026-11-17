package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261103131742
func TestMonthPlanSpans(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := month.AddDate(0, 1, 0)
	day := func(d int) time.Time { return month.Add(time.Duration(d) * 24 * time.Hour) }
	activation := func(plan api.Plan, at time.Time) state.FinancialPriceSnapshot {
		return state.FinancialPriceSnapshot{Plan: plan, EffectiveFrom: at, Price: financial.Price{Meter: "compute"}}
	}
	tests := []struct {
		name      string
		snapshots []state.FinancialPriceSnapshot
		through   time.Time
		want      []state.PlanSpan
	}{
		{"no history keeps the current plan", nil, end, []state.PlanSpan{{api.PlanHobby, month, end}}},
		{"the earliest activation covers the month before it", []state.FinancialPriceSnapshot{activation(api.PlanPro, day(5))}, end,
			[]state.PlanSpan{{api.PlanPro, month, end}}},
		{"a downgrade splits the month", []state.FinancialPriceSnapshot{activation(api.PlanPro, month), activation(api.PlanHobby, day(15))}, end,
			[]state.PlanSpan{{api.PlanPro, month, day(15)}, {api.PlanHobby, day(15), end}}},
		{"a re-activation of the same plan is one span", []state.FinancialPriceSnapshot{activation(api.PlanPro, month), activation(api.PlanPro, day(3))}, end,
			[]state.PlanSpan{{api.PlanPro, month, end}}},
		{"activations after through are ignored", []state.FinancialPriceSnapshot{activation(api.PlanPro, month), activation(api.PlanHobby, day(20))}, day(10),
			[]state.PlanSpan{{api.PlanPro, month, day(10)}}},
		{"other meters are ignored", []state.FinancialPriceSnapshot{{Plan: api.PlanScale, EffectiveFrom: month, Price: financial.Price{Meter: "egress"}}}, end,
			[]state.PlanSpan{{api.PlanHobby, month, end}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := state.MonthPlanSpans(api.PlanHobby, tt.snapshots, month, tt.through)
			if len(got) != len(tt.want) {
				t.Fatalf("spans = %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i].Plan != tt.want[i].Plan || !got[i].Start.Equal(tt.want[i].Start) || !got[i].End.Equal(tt.want[i].End) {
					t.Fatalf("spans = %+v, want %+v", got, tt.want)
				}
			}
		})
	}
}

// adr: 20261103131742
func TestWeightedMonthAllowance(t *testing.T) {
	month := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) // 30 days
	half := month.Add(15 * 24 * time.Hour)
	allowance := map[api.Plan]int64{api.PlanPro: 1000, api.PlanHobby: 200}
	lookup := func(plan api.Plan) int64 { return allowance[plan] }
	spans := []state.PlanSpan{{Plan: api.PlanPro, Start: month, End: half}, {Plan: api.PlanHobby, Start: half, End: month.AddDate(0, 1, 0)}}
	if got := state.WeightedMonthAllowance(spans, month, lookup); got != 600 {
		t.Fatalf("half Pro, half Hobby = %d, want 600", got)
	}
	whole := []state.PlanSpan{{Plan: api.PlanPro, Start: month, End: month.AddDate(0, 1, 0)}}
	if got := state.WeightedMonthAllowance(whole, month, lookup); got != 1000 {
		t.Fatalf("whole month on Pro = %d, want 1000", got)
	}
}
