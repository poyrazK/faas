package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The sizing report node_join reads carries the build and control-plane slice
// fences for the host's reserved builder slots, and the tenant fence shrinks
// by one BuilderSlotReserveMB for each slot past the first.
//
// adr: 20261118154632
func TestComputeNodesSizingBuilderReservedSlots(t *testing.T) {
	sizing := func(t *testing.T, args ...string) nodeSizingReport {
		t.Helper()
		_, restore := captureSizingStdout(t)
		code := cmdComputeNodesSizing(append(args, "--json"))
		out := restore()
		if code != 0 {
			t.Fatalf("sizing %v exit = %d, output %q", args, code, out)
		}
		var report nodeSizingReport
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &report); err != nil {
			t.Fatalf("parse report %q: %v", out, err)
		}
		return report
	}
	one := sizing(t, "--role", "compute-only")
	two := sizing(t, "--role", "compute-only", "--builder-reserved-slots", "2")
	if one.BuilderReservedSlots != 1 || one.BuildSliceMemoryMaxMB != api.BuilderSliceMaxMB || one.BuildSliceCPUQuotaPct != 200 || one.CPSliceMemoryMaxMB != api.ControlPlaneReserveMB {
		t.Fatalf("default report %+v must keep today's 1 slot, %dM/200%% build slice and %dM cp slice", one, api.BuilderSliceMaxMB, api.ControlPlaneReserveMB)
	}
	if two.BuilderReservedSlots != 2 || two.BuildSliceMemoryMaxMB != api.BuilderSliceMemoryMaxMB(2) || two.BuildSliceCPUQuotaPct != 400 || two.CPSliceMemoryMaxMB != api.ComputeCPSliceMemoryMaxMB(2) {
		t.Fatalf("two-slot report %+v", two)
	}
	if one.HostMemTotalMB > 0 && one.TenantSliceMaxMB-two.TenantSliceMaxMB != api.BuilderSlotReserveMB && two.TenantSliceMaxMB != api.MinTenantSliceMB {
		t.Fatalf("tenant slice %d -> %d, want %d MB less for the second slot", one.TenantSliceMaxMB, two.TenantSliceMaxMB, api.BuilderSlotReserveMB)
	}
	single := sizing(t, "--role", "single-box")
	if single.BuildSliceMemoryMaxMB != 0 || single.CPSliceMemoryMaxMB != 0 {
		t.Fatalf("a single box reports slice fences %+v; its slices keep the role's values", single)
	}
	for _, args := range [][]string{
		{"--role", "compute-only", "--builder-reserved-slots", "0"},
		{"--role", "compute-only", "--builder-reserved-slots", "5"},
		{"--role", "single-box", "--builder-reserved-slots", "2"},
	} {
		if code := cmdComputeNodesSizing(append(args, "--json")); code != 2 {
			t.Errorf("sizing %v exit = %d, want 2", args, code)
		}
	}
}
