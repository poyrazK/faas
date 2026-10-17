// adr: 1350 — managed PostgreSQL plan entitlements follow the October 2026
// COGS report.
package api

import "testing"

func TestManagedPostgresLimitsFor(t *testing.T) {
	const gib = int64(1 << 30)
	const cuHour = int64(60 * 60)
	const week = int64(7 * 24 * 60 * 60)
	want := map[Plan]ManagedPostgresPlanLimits{
		PlanFree: {DatabasesMax: 1, StorageLimitBytes: gib / 2, MonthlyComputeUnitSeconds: 5 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true},
		PlanHobby: {DatabasesMax: 1, StorageLimitBytes: gib, MonthlyComputeUnitSeconds: 25 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true},
		PlanPro: {DatabasesMax: 3, StorageLimitBytes: 5 * gib, MonthlyComputeUnitSeconds: 75 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true, BurstableAllowed: true},
		PlanScale: {DatabasesMax: 10, StorageLimitBytes: 20 * gib, MonthlyComputeUnitSeconds: 200 * cuHour,
			RestoreWindowSeconds: week, DevelopmentAllowed: true, BurstableAllowed: true, ProductionAllowed: true},
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
	if _, ok := ManagedPostgresLimitsFor(Plan("unknown")); ok {
		t.Fatal("unknown plan unexpectedly received managed postgres limits")
	}
}
