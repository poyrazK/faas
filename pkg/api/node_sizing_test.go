package api

import "testing"

// TestDeriveNodeSizing_ReferenceBoxReproducesSpecConstants is the load-bearing
// case: the derivation must be a generalisation of spec §13, not a new policy.
// If this drifts, every existing single-box deployment silently re-sizes.
func TestDeriveNodeSizing_ReferenceBoxReproducesSpecConstants(t *testing.T) {
	got := DeriveNodeSizing(65_536, 20)
	if got.TenantSliceMaxMB != TenantSliceMaxMB {
		t.Errorf("tenant slice = %d, want %d", got.TenantSliceMaxMB, TenantSliceMaxMB)
	}
	if got.TenantBudgetMB != TenantRAMBudgetMB {
		t.Errorf("tenant budget = %d, want %d", got.TenantBudgetMB, TenantRAMBudgetMB)
	}
	if got.AdmissionCeilingMB != RAMAdmissionCeilingMB {
		t.Errorf("admission ceiling = %d, want %d", got.AdmissionCeilingMB, RAMAdmissionCeilingMB)
	}
	if got.VCPUSlots != VCPUSlots {
		t.Errorf("vcpu slots = %d, want %d", got.VCPUSlots, VCPUSlots)
	}
}

// TestDeriveNodeSizing_SmallNodeStopsOvercommitting pins the bug this fixes:
// a 16 GiB n2-standard-4 used to advertise 56,000 MB and a 47,600 MB ceiling,
// roughly 3x its real memory, with no swap to absorb the overshoot.
func TestDeriveNodeSizing_SmallNodeStopsOvercommitting(t *testing.T) {
	const memMB = 15_986 // measured MemTotal on faas-compute-node-2
	got := DeriveNodeSizing(memMB, 4)
	if got.MemMB != memMB {
		t.Errorf("MemMB = %d, want the host's real %d", got.MemMB, memMB)
	}
	if got.AdmissionCeilingMB >= memMB {
		t.Errorf("ceiling %d MB must be below physical %d MB", got.AdmissionCeilingMB, memMB)
	}
	if got.AdmissionCeilingMB >= RAMAdmissionCeilingMB {
		t.Errorf("ceiling %d MB must be far below the single-box %d MB", got.AdmissionCeilingMB, RAMAdmissionCeilingMB)
	}
	if want := 4 * CPUOvercommit; got.VCPUSlots != want {
		t.Errorf("vcpu slots = %d, want %d (%d CPUs x %dx overcommit)", got.VCPUSlots, want, 4, CPUOvercommit)
	}
	// Reserves plus the 85% guard must leave real headroom under physical.
	if got.TenantSliceMaxMB != memMB-HostOSReserveMB-ControlPlaneReserveMB {
		t.Errorf("tenant slice = %d, want MemTotal minus both reserves", got.TenantSliceMaxMB)
	}
}

// TestDeriveNodeSizing_UndetectableHostKeepsLegacyConstants: an unknown
// machine must not silently become an unbounded one, and must still satisfy
// migration 00123's vcpu_budget > 0 CHECK so registration does not wedge.
func TestDeriveNodeSizing_UndetectableHostKeepsLegacyConstants(t *testing.T) {
	for _, mem := range []int{0, -1} {
		got := DeriveNodeSizing(mem, 0)
		if got.AdmissionCeilingMB != RAMAdmissionCeilingMB || got.TenantBudgetMB != TenantRAMBudgetMB {
			t.Errorf("memTotal=%d fell back to %+v, want the single-box constants", mem, got)
		}
		if got.VCPUSlots <= 0 || got.MemMB <= 0 {
			t.Errorf("memTotal=%d produced non-positive sizing %+v, which fails the vcpu_budget CHECK", mem, got)
		}
	}
}

// TestDeriveNodeSizing_TinyHostStillRegisters: a host smaller than the
// reserves must floor rather than derive a zero or negative ceiling.
func TestDeriveNodeSizing_TinyHostStillRegisters(t *testing.T) {
	got := DeriveNodeSizing(4_096, 2)
	if got.TenantSliceMaxMB != MinTenantSliceMB {
		t.Errorf("tenant slice = %d, want the %d MB floor", got.TenantSliceMaxMB, MinTenantSliceMB)
	}
	if got.AdmissionCeilingMB <= 0 {
		t.Errorf("ceiling = %d, must stay positive for the migration CHECK", got.AdmissionCeilingMB)
	}
}
