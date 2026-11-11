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
		{"no weight gets the cgroup default", JailerSpec{Plan: api.PlanScale}, 100},
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
	if want := fmt.Sprintf("max %d", cpuMaxPeriodUS); err != nil || got != want {
		t.Fatalf("leaseCPUMax = %q, %v; want %q", got, err, want)
	}
	if shouldApplyStartupCPUBoost(l, true) {
		t.Fatal("an uncapped VM got a startup boost")
	}
	capped := l
	capped.CPUUncapped = false
	if got, _ := leaseCPUMax(capped); got == fmt.Sprintf("max %d", cpuMaxPeriodUS) {
		t.Fatalf("a capped lease got %q", got)
	}
	agent := Lease{Plan: api.PlanPro, CPUMillicores: 2000, Networkless: true, CPUUncapped: true}
	if got, _ := leaseCPUMax(agent); got == fmt.Sprintf("max %d", cpuMaxPeriodUS) {
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
	assertFence(t, scope, memoryMaxValue(api.BillableRAMMB(512)), "32", fmt.Sprintf("max %d", cpuMaxPeriodUS))
}

// An uncapped lease drops its quota only at the app default ceiling: an app
// that chose 250 or 500 millicores keeps that cap and its startup boost, and
// a live policy update moves the VM between the two.
func TestUncappedLeaseKeepsAnAppsOwnCPUCap(t *testing.T) {
	uncapped := fmt.Sprintf("max %d", cpuMaxPeriodUS)
	half := fmt.Sprintf("%d %d", 500*cpuMaxPeriodUS/1000, cpuMaxPeriodUS)
	l := Lease{Instance: "self-capped", Plan: api.PlanPro, MemoryMaxMiB: 512, CPUMillicores: 500, CPUWeight: 32, CPUUncapped: true}
	if got, err := leaseCPUMax(l); err != nil || got != half {
		t.Fatalf("leaseCPUMax at 500 m = %q, %v; want %q", got, err, half)
	}
	if !shouldApplyStartupCPUBoost(l, true) {
		t.Fatal("a self-capped VM lost its startup boost")
	}

	v, scope := fencedVM(t, l)
	for _, step := range []struct {
		cpu  int
		want string
	}{{1000, uncapped}, {500, half}, {1000, uncapped}} {
		if err := v.UpdateCPULimit(context.Background(), l, step.cpu); err != nil {
			t.Fatalf("UpdateCPULimit(%d): %v", step.cpu, err)
		}
		if got := readFenceFile(t, scope, "cpu.max"); got != step.want {
			t.Fatalf("after a live update to %d m, cpu.max = %q, want %q", step.cpu, got, step.want)
		}
	}
	// The fence repairs to the last live value, not the boot-time one.
	if err := v.UpdateCPULimit(context.Background(), l, 500); err != nil {
		t.Fatal(err)
	}
	resetLikeSystemdReload(t, scope)
	if rep := v.RepairCgroupFences(context.Background()); rep.Repaired != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want one repair", rep)
	}
	assertFence(t, scope, memoryMaxValue(api.BillableRAMMB(512)), "32", half)
}

// No VM's CPU depends on its plan (step 6.3). An app instance gets the
// weight schedd sent, else its memory's, and no quota whatever schedd sent.
// An execution guest gets its memory's weight and keeps its quota; a
// builder keeps what it was sent.
func TestWakeLeaseCPUFollowsAppSize(t *testing.T) {
	for _, tc := range []struct {
		name         string
		req          WakeRequest
		builder      bool
		wantWeight   int
		wantUncapped bool
	}{
		{"schedd's size fields", WakeRequest{MemSizeMiB: 512, CPUWeight: 32, CPUUncapped: true}, false, 32, true},
		{"an N-1 schedd's plan request", WakeRequest{MemSizeMiB: 512}, false, 32, true},
		{"a 2 GB instance", WakeRequest{MemSizeMiB: 2048}, false, 128, true},
		{"a 128 MB instance", WakeRequest{MemSizeMiB: 128}, false, 8, true},
		{"a builder", WakeRequest{MemSizeMiB: 2048}, true, 0, false},
		{"an execution guest", WakeRequest{MemSizeMiB: 512, ExecutionOnly: true}, false, 32, false},
		{"no memory size", WakeRequest{}, false, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			weight, uncapped := wakeLeaseCPU(tc.req, tc.builder)
			if weight != tc.wantWeight || uncapped != tc.wantUncapped {
				t.Fatalf("wakeLeaseCPU = %d, %v; want %d, %v", weight, uncapped, tc.wantWeight, tc.wantUncapped)
			}
		})
	}
}
