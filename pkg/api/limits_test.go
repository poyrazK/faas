package api

import "testing"

// TestPlanLimitsMatchSpec pins every value in the table to the financial-model /
// spec §1 numbers. If the spreadsheet moves, this test must be updated in the
// same PR — that is the point.
func TestPlanLimitsMatchSpec(t *testing.T) {
	want := map[Plan]Limits{
		PlanFree:  {Plan: PlanFree, DeployedApps: 1, MaxConcurrency: 1, RAMMB: 128, AppLayerMaxMB: 256, SourceTarballMaxMB: 100, VCPU: 2, IdleTimeoutS: 30, IncludedGBHours: 5, PriceMillicents: 0, RateLimitRPS: 5, RateLimitBurst: 20, EgressMbit: 10},
		PlanHobby: {Plan: PlanHobby, DeployedApps: 5, MaxConcurrency: 2, RAMMB: 256, AppLayerMaxMB: 512, SourceTarballMaxMB: 100, VCPU: 2, IdleTimeoutS: 60, IncludedGBHours: 50, PriceMillicents: 900_000, RateLimitRPS: 20, RateLimitBurst: 100, EgressMbit: 25},
		PlanPro:   {Plan: PlanPro, DeployedApps: 25, MaxConcurrency: 5, RAMMB: 512, AppLayerMaxMB: 1024, SourceTarballMaxMB: 250, VCPU: 2, IdleTimeoutS: 300, IncludedGBHours: 250, PriceMillicents: 2_900_000, RateLimitRPS: 100, RateLimitBurst: 500, EgressMbit: 100},
		PlanScale: {Plan: PlanScale, DeployedApps: 100, MaxConcurrency: 20, RAMMB: 1024, AppLayerMaxMB: 2048, SourceTarballMaxMB: 250, VCPU: 4, IdleTimeoutS: 600, IncludedGBHours: 1500, PriceMillicents: 9_900_000, RateLimitRPS: 500, RateLimitBurst: 2000, EgressMbit: 250},
	}
	for _, p := range Plans {
		got := MustLimitsFor(p)
		if got != want[p] {
			t.Errorf("limits for %s:\n got  %+v\n want %+v", p, got, want[p])
		}
	}
}

func TestPlansTableCoverage(t *testing.T) {
	if len(Plans) != len(planLimits) {
		t.Fatalf("Plans list (%d) and planLimits table (%d) out of sync", len(Plans), len(planLimits))
	}
	for _, p := range Plans {
		if _, ok := planLimits[p]; !ok {
			t.Errorf("plan %s in Plans but missing from planLimits", p)
		}
	}
}

// TestAdmissionCeilingIs85Percent guards the headroom invariant (§6.2-2): schedd
// admits to 85% of the 56 GB tenant budget.
func TestAdmissionCeilingIs85Percent(t *testing.T) {
	// 0.85 * 56000 = 47600 exactly. Do the check in integers to avoid floats.
	if got := TenantRAMBudgetMB * 85 / 100; got != RAMAdmissionCeilingMB {
		t.Errorf("RAMAdmissionCeilingMB = %d, want 85%% of %d = %d", RAMAdmissionCeilingMB, TenantRAMBudgetMB, got)
	}
	if RAMAdmissionCeilingMB >= TenantSliceMaxMB {
		t.Errorf("admission ceiling %d must sit below the hard slice fence %d", RAMAdmissionCeilingMB, TenantSliceMaxMB)
	}
}

// TestPlansAreMonotonic asserts every quota grows (or holds) from Free→Scale, so
// an upgrade never reduces a customer's allowance.
func TestPlansAreMonotonic(t *testing.T) {
	for i := 1; i < len(Plans); i++ {
		lo := MustLimitsFor(Plans[i-1])
		hi := MustLimitsFor(Plans[i])
		checks := []struct {
			name   string
			lo, hi int
		}{
			{"DeployedApps", lo.DeployedApps, hi.DeployedApps},
			{"MaxConcurrency", lo.MaxConcurrency, hi.MaxConcurrency},
			{"RAMMB", lo.RAMMB, hi.RAMMB},
			{"AppLayerMaxMB", lo.AppLayerMaxMB, hi.AppLayerMaxMB},
			{"IncludedGBHours", lo.IncludedGBHours, hi.IncludedGBHours},
			{"IdleTimeoutS", lo.IdleTimeoutS, hi.IdleTimeoutS},
			{"RateLimitRPS", lo.RateLimitRPS, hi.RateLimitRPS},
			{"EgressMbit", lo.EgressMbit, hi.EgressMbit},
		}
		for _, c := range checks {
			if c.hi < c.lo {
				t.Errorf("%s not monotonic: %s=%d < %s=%d", c.name, Plans[i], c.hi, Plans[i-1], c.lo)
			}
		}
		if hi.PriceMillicents < lo.PriceMillicents {
			t.Errorf("price not monotonic: %s=%d < %s=%d", Plans[i], hi.PriceMillicents, Plans[i-1], lo.PriceMillicents)
		}
	}
}

func TestAdmissionMB(t *testing.T) {
	for _, p := range Plans {
		l := MustLimitsFor(p)
		if got, want := l.AdmissionMB(), l.RAMMB+PerVMOverheadMB; got != want {
			t.Errorf("%s AdmissionMB()=%d want %d", p, got, want)
		}
	}
}

func TestIdleTimeoutBounds(t *testing.T) {
	l := MustLimitsFor(PlanPro) // default 300s
	floor, ceiling := l.IdleTimeoutBounds()
	if floor != IdleTimeoutFloorSeconds {
		t.Errorf("floor=%d want %d", floor, IdleTimeoutFloorSeconds)
	}
	if ceiling != 600 {
		t.Errorf("ceiling=%d want 600 (300 * %d)", ceiling, IdleTimeoutMaxMultiple)
	}
}

func TestPlanValidity(t *testing.T) {
	for _, p := range Plans {
		if !p.Valid() {
			t.Errorf("plan %s should be valid", p)
		}
	}
	if Plan("enterprise").Valid() {
		t.Error(`"enterprise" should not be a valid plan`)
	}
	if Plan("").Valid() {
		t.Error("empty plan should not be valid")
	}
	if _, ok := LimitsFor(Plan("nope")); ok {
		t.Error("LimitsFor unknown plan should return ok=false")
	}
}

func TestMustLimitsForPanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("MustLimitsFor should panic on unknown plan")
		}
	}()
	MustLimitsFor(Plan("nope"))
}
