package api

import "testing"

// TestPlanLimitsMatchSpec pins every value in the table to the financial-model /
// spec §1 numbers. If the spreadsheet moves, this test must be updated in the
// same PR — that is the point.
func TestPlanLimitsMatchSpec(t *testing.T) {
	want := map[Plan]Limits{
		// Move 1: Free gates async_invoke and queues (spec §4.4 paid-only).
		// EgressAllowlistAllowed/MaxSize default to false/0 (Go zero), so
		// Free/Hobby rows below omit them intentionally — mirrors the
		// MinInstancesAllowed row shape.
		PlanFree: {Plan: PlanFree, DeployedApps: 1, MaxConcurrency: 1, RAMMB: 128, AppLayerMaxMB: 256, SourceTarballMaxMB: 100, VCPU: 2, IdleTimeoutS: 30, IncludedGBHours: 5, PriceMillicents: 0, RateLimitRPS: 5, RateLimitBurst: 20, EgressMbit: 10, SecretCountMax: 3, SecretValueMaxBytes: 4096,
			MaxQueueDepth: 0, MaxDelayedTasksPerApp: 0, MaxSourceBytesPerInvocation: 0, AsyncInvokeAllowed: false},
		PlanHobby: {Plan: PlanHobby, DeployedApps: 5, MaxConcurrency: 2, RAMMB: 256, AppLayerMaxMB: 512, SourceTarballMaxMB: 100, VCPU: 2, IdleTimeoutS: 60, IncludedGBHours: 50, PriceMillicents: 900_000, RateLimitRPS: 20, RateLimitBurst: 100, EgressMbit: 25, SecretCountMax: 25, SecretValueMaxBytes: 8192,
			MaxQueueDepth: 5, MaxDelayedTasksPerApp: 5, MaxSourceBytesPerInvocation: 64 * 1024, AsyncInvokeAllowed: true},
		// ADR-031: Pro opt-in for per-app egress allowlist with a 16-CIDR cap.
		PlanPro: {Plan: PlanPro, DeployedApps: 25, MaxConcurrency: 5, RAMMB: 512, AppLayerMaxMB: 1024, SourceTarballMaxMB: 250, VCPU: 2, IdleTimeoutS: 300, IncludedGBHours: 250, PriceMillicents: 2_900_000, RateLimitRPS: 100, RateLimitBurst: 500, EgressMbit: 100, SecretCountMax: 50, SecretValueMaxBytes: 16384, MinInstancesAllowed: true,
			MaxQueueDepth: 25, MaxDelayedTasksPerApp: 50, MaxSourceBytesPerInvocation: 256 * 1024, AsyncInvokeAllowed: true,
			EgressAllowlistAllowed: true, EgressAllowlistMaxSize: 16},
		// ADR-031: Scale double-up to 64 CIDR cap (2× Pro, tracks 2×
		// DeployedApps).
		PlanScale: {Plan: PlanScale, DeployedApps: 100, MaxConcurrency: 20, RAMMB: 1024, AppLayerMaxMB: 2048, SourceTarballMaxMB: 250, VCPU: 4, IdleTimeoutS: 600, IncludedGBHours: 1500, PriceMillicents: 9_900_000, RateLimitRPS: 500, RateLimitBurst: 2000, EgressMbit: 250, SecretCountMax: 100, SecretValueMaxBytes: 32768, MinInstancesAllowed: true,
			MaxQueueDepth: 100, MaxDelayedTasksPerApp: 1_000_000, MaxSourceBytesPerInvocation: 1024 * 1024, AsyncInvokeAllowed: true,
			EgressAllowlistAllowed: true, EgressAllowlistMaxSize: 64},
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

// TestPlanMinInstancesAllowed pins the per-plan gate that apid's
// updateApp handler uses for ux_spec §6.5. Free/Hobby → false
// (always scale to zero); Pro/Scale → true. Unknown plans must
// default to false (fail-closed: a missing plan never silently
// unlocks a premium feature).
func TestPlanMinInstancesAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.MinInstancesAllowed(); got != c.want {
			t.Errorf("%s.MinInstancesAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanEgressAllowlistAllowed pins the per-plan gate that apid's
// updateApp handler uses for the per-app egress allowlist (ADR-031).
// Free/Hobby → false (no allowlist — abuse-desk hygiene is a Pro+
// concern; the default scale-to-zero tenant never sees this surface);
// Pro/Scale → true. Unknown plans must default to false (fail-closed
// — same contract as MinInstancesAllowed above).
func TestPlanEgressAllowlistAllowed(t *testing.T) {
	cases := []struct {
		plan Plan
		want bool
	}{
		{PlanFree, false},
		{PlanHobby, false},
		{PlanPro, true},
		{PlanScale, true},
		{Plan("unknown"), false},
	}
	for _, c := range cases {
		if got := c.plan.EgressAllowlistAllowed(); got != c.want {
			t.Errorf("%s.EgressAllowlistAllowed() = %v, want %v", c.plan, got, c.want)
		}
	}
}

// TestPlanEgressAllowlistMaxSize pins the per-plan CIDR cap (ADR-031).
// Free/Hobby → 0 (no allowlist slot, the gate above rejects the
// PATCH before this matters); Pro → 16; Scale → 64.
func TestPlanEgressAllowlistMaxSize(t *testing.T) {
	cases := []struct {
		plan Plan
		want int
	}{
		{PlanFree, 0},
		{PlanHobby, 0},
		{PlanPro, 16},
		{PlanScale, 64},
	}
	for _, c := range cases {
		if got := c.plan.EgressAllowlistMaxSize(); got != c.want {
			t.Errorf("%s.EgressAllowlistMaxSize() = %d, want %d", c.plan, got, c.want)
		}
	}
}

// TestPlanEgressAllowlistMonotonic pins the Pro→Scale ordering so a
// future bump that flips the ratio (e.g. Scale 32 < Pro 64) is caught
// here. Mirrors the TestPlansAreMonotonic style — Pro MaxSize must be
// ≤ Scale MaxSize because Scale is the bigger tier.
func TestPlanEgressAllowlistMonotonic(t *testing.T) {
	pro := MustLimitsFor(PlanPro).EgressAllowlistMaxSize
	scale := MustLimitsFor(PlanScale).EgressAllowlistMaxSize
	if scale < pro {
		t.Errorf("Scale EgressAllowlistMaxSize=%d < Pro=%d — Scale must keep the larger CIDR budget", scale, pro)
	}
}

// TestOCIPullTimeoutSeconds pins the per-pull HTTP timeout (ADR-021) —
// pkg/oci.RegistryClient consults this when no WithTimeout override is
// passed. The number is a platform constant: every plan shares the same
// ceiling so the cold-boot latency contract stays predictable. 60s is
// well above the largest manifest + image-config GET and a generous
// safety margin over the fail-fast PullImageConfig path.
func TestOCIPullTimeoutSeconds(t *testing.T) {
	if OCIPullTimeoutSeconds != 60 {
		t.Errorf("OCIPullTimeoutSeconds = %d, want 60", OCIPullTimeoutSeconds)
	}
	if OCIPullTimeoutSeconds < 10 {
		t.Errorf("OCIPullTimeoutSeconds = %d must be >= 10s so a slow registry cannot starve the cold-boot latency budget", OCIPullTimeoutSeconds)
	}
}
