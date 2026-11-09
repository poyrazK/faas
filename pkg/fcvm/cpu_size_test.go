// adr: 20261109172421

package fcvm

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// An app VM takes the cpu.weight schedd sends instead of its plan's; a
// builder and a request without one keep the old weights.
func TestJailerCommandTakesTheInstanceCPUWeight(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec JailerSpec
		want int
	}{
		{"instance weight", JailerSpec{Plan: api.PlanFree, CPUWeight: 32}, 32},
		{"clamped to the cgroup maximum", JailerSpec{Plan: api.PlanFree, CPUWeight: 20000}, MaxCPUWeight},
		{"no weight keeps the plan's", JailerSpec{Plan: api.PlanScale}, api.PlanScale.CPUWeight()},
		{"a builder keeps the neutral weight", JailerSpec{Plan: api.PlanPro, IsBuilder: true, CPUWeight: 32}, 256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.spec.Instance, tc.spec.UID, tc.spec.GID, tc.spec.Netns = "app-1", 20001, 20001, "fc-app-1"
			want := fmt.Sprintf("cpu.weight=%d", tc.want)
			if argv := JailerCommand(tc.spec); !slices.Contains(argv, want) {
				t.Fatalf("argv %v lacks %s", argv, want)
			}
		})
	}
}

// An uncapped app VM gets cpu.max = max and no startup boost; agent runs and
// builders keep their own quotas.
func TestUncappedLeaseHasNoCPUQuotaAndNoBoost(t *testing.T) {
	l := Lease{Plan: api.PlanPro, CPUMillicores: 1000, CPUUncapped: true}
	got, err := leaseCPUMax(l)
	if want := fmt.Sprintf("max %d", api.PlanPro.CPUPeriodUS()); err != nil || got != want {
		t.Fatalf("leaseCPUMax = %q, %v; want %q", got, err, want)
	}
	if shouldApplyStartupCPUBoost(l, true) {
		t.Fatal("an uncapped VM got a startup boost")
	}
	capped := l
	capped.CPUUncapped = false
	if got, _ := leaseCPUMax(capped); got == fmt.Sprintf("max %d", api.PlanPro.CPUPeriodUS()) {
		t.Fatalf("a capped lease got %q", got)
	}
	agent := Lease{Plan: api.PlanPro, CPUMillicores: 2000, Networkless: true, CPUUncapped: true}
	if got, _ := leaseCPUMax(agent); got == fmt.Sprintf("max %d", api.PlanPro.CPUPeriodUS()) {
		t.Fatalf("an agent run lost its quota: %q", got)
	}
}

// After a systemd reload the fence puts back the instance's own weight and
// its uncapped cpu.max, not the plan's.
func TestRepairCgroupFencesKeepsTheInstanceWeightAndNoQuota(t *testing.T) {
	l := Lease{Instance: "uncapped-after-reload", Plan: api.PlanScale, MemoryMaxMiB: 512, CPUWeight: 32, CPUUncapped: true}
	v, scope := fencedVM(t, l)
	resetLikeSystemdReload(t, scope)
	if rep := v.RepairCgroupFences(context.Background()); rep.Repaired != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want one repair", rep)
	}
	assertFence(t, scope, memoryMaxValue(api.BillableRAMMB(512)), "32", fmt.Sprintf("max %d", api.PlanScale.CPUPeriodUS()))
}
