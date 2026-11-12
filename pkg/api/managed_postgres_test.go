// adr: 1350 — managed PostgreSQL plan entitlements follow the October 2026
// COGS report.
// adr: 20261110184700 — the Free plan includes no managed PostgreSQL.
package api

import (
	"slices"
	"testing"
)

func TestManagedPostgresLimitsFor(t *testing.T) {
	const gib = int64(1 << 30)
	const cuHour = int64(60 * 60)
	const week = int64(7 * 24 * 60 * 60)
	want := map[Plan]ManagedPostgresPlanLimits{
		PlanFree: {NotIncluded: true},
		PlanHobby: {DatabasesMax: 1, StorageLimitBytes: gib, MonthlyComputeUnitSeconds: 25 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true},
		PlanPro: {DatabasesMax: 3, StorageLimitBytes: 5 * gib, MonthlyComputeUnitSeconds: 75 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true, BurstableAllowed: true},
		PlanScale: {DatabasesMax: 10, StorageLimitBytes: 20 * gib, MonthlyComputeUnitSeconds: 200 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true, BurstableAllowed: true, ProductionAllowed: true, OverageOnByDefault: true},
	}
	for plan, limits := range want {
		got, ok := ManagedPostgresLimitsFor(plan)
		if !ok || got != limits {
			t.Fatalf("%s managed postgres limits = %+v (ok=%v), want %+v", plan, got, ok, limits)
		}
		if got.AlwaysOnAllowed {
			t.Fatalf("%s allows always-on compute; no plan includes it", plan)
		}
	}
	for plan, included := range map[Plan]bool{PlanFree: false, PlanHobby: true, PlanPro: true, PlanScale: true, Plan("unknown"): false} {
		if got := ManagedPostgresIncluded(plan); got != included {
			t.Fatalf("ManagedPostgresIncluded(%s) = %v, want %v", plan, got, included)
		}
	}
	if _, ok := ManagedPostgresLimitsFor(Plan("unknown")); ok {
		t.Fatal("unknown plan unexpectedly received managed postgres limits")
	}
}

// Overage is the provider's price plus the markup, never below what Gregale
// pays (ADR-20261102072425).
func TestManagedPostgresOverageIsTheProviderPricePlusTheMarkup(t *testing.T) {
	if ManagedPostgresOverageMillicentsPerComputeUnitHour != 23_310 {
		t.Fatalf("compute overage = %d millicents per CU-h, want 23310 ($0.222 + 5%%)", ManagedPostgresOverageMillicentsPerComputeUnitHour)
	}
	if ManagedPostgresOverageMillicentsPerComputeUnitHour*100 != ManagedPostgresNeonScaleMillicentsPerComputeUnitHour*(100+ManagedPostgresOverageMarkupPercent) {
		t.Fatal("the compute overage price loses a fraction of a millicent to rounding")
	}
}

// ADR-20261025094915 amendment 2026-11-11: compute overage is on by default
// only on Scale, and a subject's explicit choice always wins.
func TestManagedPostgresOverageEffective(t *testing.T) {
	if got := ManagedPostgresOverageDefaultPlans(); !slices.Equal(got, []string{string(PlanScale)}) {
		t.Fatalf("default-on plans = %v, want [scale]", got)
	}
	for _, tc := range []struct {
		plan            Plan
		enabled, chosen bool
		want            bool
	}{
		{PlanScale, false, false, true},
		{PlanScale, false, true, false},
		{PlanPro, false, false, false},
		{PlanPro, true, true, true},
		{PlanHobby, false, false, false},
		{PlanFree, false, false, false},
		{Plan("unknown"), false, false, false},
	} {
		if got := ManagedPostgresOverageEffective(tc.plan, tc.enabled, tc.chosen); got != tc.want {
			t.Errorf("%s enabled=%v chosen=%v = %v, want %v", tc.plan, tc.enabled, tc.chosen, got, tc.want)
		}
	}
}
