package productcontrol

import (
	"testing"
	"time"
)

// adr: 20261021154511 — a month owes the highest fee among the plans held in
// it, once; only the month of the first assignment is prorated, by the
// minute and rounded half up; plans without a fee owe nothing.
func TestProductMonthlyFee(t *testing.T) {
	fees := map[string]int64{"r1/starter": 1000000, "r1/pro": 2900000, "r2/pro": 2900000, "r1/free": 0}
	fee := func(revision, plan string) int64 { return fees[revision+"/"+plan] }
	oct := func(day, hour, minute int) time.Time { return time.Date(2026, 10, day, hour, minute, 0, 0, time.UTC) }
	for _, tc := range []struct {
		name         string
		held         []PlanHeld
		plan         string
		amount       int64
		proratedFrom *time.Time
	}{
		{name: "no plans"},
		{name: "free plan", held: []PlanHeld{{Revision: "r1", Plan: "free", Since: oct(1, 0, 0)}}},
		{name: "starter all month", held: []PlanHeld{{Revision: "r1", Plan: "starter", Since: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), First: true}}, plan: "starter", amount: 1000000},
		{name: "upgrade and downgrade owe the higher fee in full", held: []PlanHeld{
			{Revision: "r1", Plan: "starter", Since: oct(1, 0, 0)}, {Revision: "r1", Plan: "pro", Since: oct(20, 9, 0)}, {Revision: "r1", Plan: "starter", Since: oct(21, 9, 0)}},
			plan: "pro", amount: 2900000},
		// 15,840 of October's 44,640 minutes: 2,900,000 × 15,840 / 44,640 = 1,029,032.26.
		{name: "first month prorated", held: []PlanHeld{{Revision: "r1", Plan: "pro", Since: oct(21, 0, 0), First: true}}, plan: "pro", amount: 1029032, proratedFrom: ptr(oct(21, 0, 0))},
		{name: "joined at the month start owes it whole", held: []PlanHeld{{Revision: "r1", Plan: "pro", Since: oct(1, 0, 0), First: true}}, plan: "pro", amount: 2900000},
		{name: "joined on free then upgraded is prorated from joining", held: []PlanHeld{
			{Revision: "r1", Plan: "free", Since: oct(21, 0, 0), First: true}, {Revision: "r1", Plan: "pro", Since: oct(25, 0, 0)}}, plan: "pro", amount: 1029032, proratedFrom: ptr(oct(21, 0, 0))},
		{name: "seconds are dropped from the join", held: []PlanHeld{{Revision: "r1", Plan: "pro", Since: oct(21, 0, 0).Add(59 * time.Second), First: true}}, plan: "pro", amount: 1029032, proratedFrom: ptr(oct(21, 0, 0))},
		{name: "equal fees name the earliest", held: []PlanHeld{{Revision: "r2", Plan: "pro", Since: oct(2, 0, 0)}, {Revision: "r1", Plan: "pro", Since: oct(1, 0, 0)}}, plan: "pro", amount: 2900000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MonthlyFee(tc.held, fee, "2026-10", nil)
			if tc.plan == "" {
				if got != nil {
					t.Fatalf("fee = %+v, want none", got)
				}
				return
			}
			if got == nil || got.Plan != tc.plan || got.AmountMillicents != tc.amount || got.MonthlyFeeMillicents != fees[got.Revision+"/"+got.Plan] ||
				(got.ProratedFrom == nil) != (tc.proratedFrom == nil) || got.ProratedFrom != nil && !got.ProratedFrom.Equal(*tc.proratedFrom) {
				t.Fatalf("fee = %+v, want plan %s amount %d prorated from %v", got, tc.plan, tc.amount, tc.proratedFrom)
			}
			if tc.name == "equal fees name the earliest" && got.Revision != "r1" {
				t.Fatalf("tie named revision %s", got.Revision)
			}
		})
	}
	if MonthlyFee([]PlanHeld{{Revision: "r1", Plan: "pro"}}, fee, "october", nil) != nil {
		t.Fatal("invalid period charged a fee")
	}
}

func ptr(t time.Time) *time.Time { return &t }

// adr: 20261021195901 — a month's fee covers only its minutes outside the
// trial: a month inside it owes nothing, the month it ends is prorated from
// its end, a plan held only during the trial does not set the fee, and a
// trial in the middle of a month leaves both sides billable.
func TestProductMonthlyFeeExcludesTrial(t *testing.T) {
	fees := map[string]int64{"r1/starter": 1000000, "r1/pro": 2900000}
	fee := func(revision, plan string) int64 { return fees[revision+"/"+plan] }
	at := func(month time.Month, day int) time.Time { return time.Date(2026, month, day, 0, 0, 0, 0, time.UTC) }
	joinedTrial := &Trial{Start: at(10, 21), End: at(11, 4)}
	pro := []PlanHeld{{Revision: "r1", Plan: "pro", Since: at(10, 21), First: true}}
	if got := MonthlyFee(pro, fee, "2026-10", joinedTrial); got != nil {
		t.Fatalf("a month inside the trial owes %+v", got)
	}
	// 27 of November's 30 days: 2,900,000 × 27 / 30.
	got := MonthlyFee(pro, fee, "2026-11", joinedTrial)
	if got == nil || got.Plan != "pro" || got.AmountMillicents != 2610000 || got.ProratedFrom != nil ||
		got.TrialStartsAt == nil || !got.TrialStartsAt.Equal(at(11, 1)) || got.TrialEndsAt == nil || !got.TrialEndsAt.Equal(at(11, 4)) {
		t.Fatalf("month the trial ends = %+v", got)
	}
	downgraded := []PlanHeld{{Revision: "r1", Plan: "pro", Since: at(10, 21), Until: at(11, 2), First: true}, {Revision: "r1", Plan: "starter", Since: at(11, 2)}}
	if got := MonthlyFee(downgraded, fee, "2026-11", joinedTrial); got == nil || got.Plan != "starter" || got.AmountMillicents != 900000 {
		t.Fatalf("downgraded during the trial = %+v", got)
	}
	// Starter until the trial of pro from the 10th to the 24th: 9 + 8 of
	// October's 31 days at pro's fee, 2,900,000 × 17 / 31 rounded half up.
	midlife := []PlanHeld{{Revision: "r1", Plan: "starter", Since: at(9, 1), Until: at(10, 10), First: true}, {Revision: "r1", Plan: "pro", Since: at(10, 10)}}
	if got := MonthlyFee(midlife, fee, "2026-10", &Trial{Start: at(10, 10), End: at(10, 24)}); got == nil || got.Plan != "pro" || got.AmountMillicents != 1590323 {
		t.Fatalf("trial in the middle of a month = %+v", got)
	}
}
