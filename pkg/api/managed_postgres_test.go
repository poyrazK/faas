package api

import "testing"

func TestManagedPostgresLimitsFor(t *testing.T) {
	hobby, ok := ManagedPostgresLimitsFor(PlanHobby)
	if !ok || hobby.DatabasesMax != 1 || hobby.StorageLimitBytes != 10*(1<<30) || hobby.RestoreWindowSeconds != 7*24*60*60 || !hobby.DevelopmentAllowed || hobby.BurstableAllowed || hobby.ProductionAllowed {
		t.Fatalf("unexpected hobby managed postgres limits: %+v, ok=%v", hobby, ok)
	}
	pro, _ := ManagedPostgresLimitsFor(PlanPro)
	if pro.DatabasesMax != 3 || pro.StorageLimitBytes != 50*(1<<30) || !pro.BurstableAllowed || pro.ProductionAllowed {
		t.Fatalf("unexpected pro managed postgres limits: %+v", pro)
	}
	scale, _ := ManagedPostgresLimitsFor(PlanScale)
	if scale.DatabasesMax != 10 || scale.StorageLimitBytes != 100*(1<<30) || !scale.ProductionAllowed {
		t.Fatalf("unexpected scale managed postgres limits: %+v", scale)
	}
	free, _ := ManagedPostgresLimitsFor(PlanFree)
	if free.DatabasesMax != 0 || free.StorageLimitBytes != 0 {
		t.Fatalf("free must not include managed postgres: %+v", free)
	}
	if _, ok := ManagedPostgresLimitsFor(Plan("unknown")); ok {
		t.Fatal("unknown plan unexpectedly received managed postgres limits")
	}
}
