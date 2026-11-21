package api

import "testing"

// One reserved builder slot is exactly today's fences, and each slot past the
// first adds one builder fence to faas-cp-build.slice and faas-cp.slice, two
// CPUs to the build slice, and BuilderSlotReserveMB to the tenant hold-back
// (ADR-20261118154632 amendment 1).
//
// adr: 20261118154632
func TestBuilderReservedSlotSizing(t *testing.T) {
	for _, tc := range []struct {
		reserved                     int
		buildSliceMB, buildCPUPct    int
		cpSliceMB, extraTenantHoldMB int
	}{
		{reserved: 0, buildSliceMB: 5_120, buildCPUPct: 200, cpSliceMB: 6_144},
		{reserved: 1, buildSliceMB: 5_120, buildCPUPct: 200, cpSliceMB: 6_144},
		{reserved: 2, buildSliceMB: 7_936, buildCPUPct: 400, cpSliceMB: 8_960, extraTenantHoldMB: 2_048},
		{reserved: 4, buildSliceMB: 13_568, buildCPUPct: 800, cpSliceMB: 14_592, extraTenantHoldMB: 6_144},
		{reserved: 9, buildSliceMB: 13_568, buildCPUPct: 800, cpSliceMB: 14_592, extraTenantHoldMB: 6_144},
	} {
		if got := BuilderSliceMemoryMaxMB(tc.reserved); got != tc.buildSliceMB {
			t.Errorf("BuilderSliceMemoryMaxMB(%d) = %d, want %d", tc.reserved, got, tc.buildSliceMB)
		}
		if got := BuilderSliceCPUQuotaPct(tc.reserved); got != tc.buildCPUPct {
			t.Errorf("BuilderSliceCPUQuotaPct(%d) = %d, want %d", tc.reserved, got, tc.buildCPUPct)
		}
		if got := ComputeCPSliceMemoryMaxMB(tc.reserved); got != tc.cpSliceMB {
			t.Errorf("ComputeCPSliceMemoryMaxMB(%d) = %d, want %d", tc.reserved, got, tc.cpSliceMB)
		}
		// The build slice must stay inside its parent with the same daemon
		// headroom one slot has today.
		if got, want := ComputeCPSliceMemoryMaxMB(tc.reserved)-BuilderSliceMemoryMaxMB(tc.reserved), ControlPlaneReserveMB-BuilderSliceMaxMB; got != want {
			t.Errorf("reserved %d leaves %d MiB for daemons in faas-cp.slice, want %d", tc.reserved, got, want)
		}
		for _, shape := range []NodeRoleShape{NodeShapeComputeOnly, NodeShapeComputeEdge, NodeShapeComputeEdgeObjectGateway} {
			one := DeriveNodeSizingForRole(131_000, 16, shape)
			got := DeriveNodeSizingForRoleWithBuilders(131_000, 16, shape, tc.reserved)
			if hold := got.NonTenantReserveMB - one.NonTenantReserveMB; hold != tc.extraTenantHoldMB {
				t.Errorf("shape %v reserved %d holds back %d MB more, want %d", shape, tc.reserved, hold, tc.extraTenantHoldMB)
			}
			if shrink := one.TenantSliceMaxMB - got.TenantSliceMaxMB; shrink != tc.extraTenantHoldMB {
				t.Errorf("shape %v reserved %d shrinks the tenant slice by %d MB, want %d", shape, tc.reserved, shrink, tc.extraTenantHoldMB)
			}
		}
	}
	if BuilderSliceMemoryMaxMB(1) != BuilderSliceMaxMB {
		t.Fatalf("one reserved slot must be today's BuilderSliceMaxMB %d", BuilderSliceMaxMB)
	}
	single := DeriveNodeSizingForRole(65_536, 20, NodeShapeSingleBox)
	if got := DeriveNodeSizingForRoleWithBuilders(65_536, 20, NodeShapeSingleBox, 3); got != single {
		t.Fatalf("a single box ignores builder slots: got %+v, want %+v", got, single)
	}
}
