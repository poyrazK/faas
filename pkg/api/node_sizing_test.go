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

// TestDeriveNodeSizingForRole_ReferenceBoxIsUnchanged pins that making the
// reserve role-aware did not move the spec §13 numbers on the 64 GB box.
//
// adr: 203
// spec: §13
func TestDeriveNodeSizingForRole_ReferenceBoxIsUnchanged(t *testing.T) {
	for _, shape := range []struct {
		name  string
		shape NodeRoleShape
		want  NodeSizing
	}{
		{"single-box", NodeShapeSingleBox, NodeSizing{
			MemMB: 65_536, TenantSliceMaxMB: 57_344, TenantBudgetMB: 56_000,
			AdmissionCeilingMB: 47_600, VCPUSlots: 160, NonTenantReserveMB: 8_192,
			// vpcpus is physical (ADR-204); 20 cores * CPUOvercommit = 160 slots.
			HostCPUs: 20,
		}},
	} {
		got := DeriveNodeSizingForRole(65_536, 20, shape.shape)
		if got != shape.want {
			t.Fatalf("%s: got %+v, want %+v", shape.name, got, shape.want)
		}
	}
	// The exported wrapper must stay the single-box shape.
	if DeriveNodeSizing(65_536, 20) != DeriveNodeSizingForRole(65_536, 20, NodeShapeSingleBox) {
		t.Fatal("DeriveNodeSizing must equal the single-box role shape")
	}
}

// TestDeriveNodeSizingForRole_ComputeOnlyRecoversControlPlaneReserve is the
// point of ADR-203: a compute host must not be charged for Postgres, apid,
// meterd, githubd, or gatewayd-public, none of which may start there.
//
// adr: 203
// spec: §13
func TestDeriveNodeSizingForRole_ComputeOnlyRecoversControlPlaneReserve(t *testing.T) {
	const prodMemMB = 15_985 // the real MemTotal on the production nodes

	single := DeriveNodeSizingForRole(prodMemMB, 4, NodeShapeSingleBox)
	compute := DeriveNodeSizingForRole(prodMemMB, 4, NodeShapeComputeOnly)

	if compute.NonTenantReserveMB >= single.NonTenantReserveMB {
		t.Fatalf("compute-only reserve %d must be below single-box %d",
			compute.NonTenantReserveMB, single.NonTenantReserveMB)
	}
	// The recovered RAM is exactly the control-plane daemons this host does
	// not run, and it must land in the tenant slice rather than vanish.
	recovered := single.NonTenantReserveMB - compute.NonTenantReserveMB
	wantRecovered := ControlPlaneReserveMB - (ComputeNodeDaemonReserveMB + BuilderSlotReserveMB)
	if recovered != wantRecovered {
		t.Fatalf("recovered %d MB, want %d MB", recovered, wantRecovered)
	}
	if got := compute.TenantSliceMaxMB - single.TenantSliceMaxMB; got != recovered {
		t.Fatalf("tenant slice grew by %d MB, want the full %d MB recovered", got, recovered)
	}
	if compute.AdmissionCeilingMB <= single.AdmissionCeilingMB {
		t.Fatalf("compute-only ceiling %d must exceed single-box %d",
			compute.AdmissionCeilingMB, single.AdmissionCeilingMB)
	}
	// The ceiling must stay strictly under the slice fence: schedd admits
	// below the kernel's hard limit, never up to it. This is the invariant
	// whose violation produced the CONSTRAINT_MEMCG kills.
	if compute.AdmissionCeilingMB >= compute.TenantSliceMaxMB {
		t.Fatalf("ceiling %d must stay under the tenant slice fence %d",
			compute.AdmissionCeilingMB, compute.TenantSliceMaxMB)
	}
	// Nothing may be promised twice: reserve + slice cannot exceed the host.
	if total := compute.NonTenantReserveMB + compute.TenantSliceMaxMB; total > prodMemMB {
		t.Fatalf("reserve+slice = %d MB overcommits a %d MB host", total, prodMemMB)
	}
}

// TestDeriveNodeSizingForRole_UndetectedHostKeepsSafeConstants pins that a
// failed probe does not silently become an unbounded node.
//
// adr: 203
// spec: §13
func TestDeriveNodeSizingForRole_UndetectedHostKeepsSafeConstants(t *testing.T) {
	got := DeriveNodeSizingForRole(0, 0, NodeShapeComputeOnly)
	if got.AdmissionCeilingMB != RAMAdmissionCeilingMB || got.TenantSliceMaxMB != TenantSliceMaxMB {
		t.Fatalf("undetected host must keep the legacy constants, got %+v", got)
	}
}
