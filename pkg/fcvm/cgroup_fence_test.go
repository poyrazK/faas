// spec: §11 — every VM keeps its memory.max / cpu fence, including after a
// systemd daemon-reload resets the parent slice's controllers.
package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// fencedVM applies the pre-boot fence for l under a fake cgroup root and
// returns the VMM and the VM scope path. jailer, not vmmd, writes cpu.weight
// when it creates the scope, so the fixture seeds it the same way.
func fencedVM(t *testing.T, l Lease) (*JailerVMM, string) {
	t.Helper()
	dir := withFakeCgroupRoot(t)
	scope := filepath.Join(dir, vmCgroupParent(l), PerInstanceScope(l.Instance))
	if err := os.MkdirAll(scope, 0o755); err != nil {
		t.Fatalf("setup scope: %v", err)
	}
	writeCgroupFile(t, scope, "cpu.weight", itoa(vmCPUWeight(l.Plan, l.IsBuilder)))
	v := &JailerVMM{}
	if err := v.applyPreBootCgroupFence(l, nil); err != nil {
		t.Fatalf("applyPreBootCgroupFence: %v", err)
	}
	return v, scope
}

func readFenceFile(t *testing.T, scope, name string) string {
	t.Helper()
	return strings.TrimSpace(readCgroupFile(t, scope, name))
}

// resetLikeSystemdReload leaves the files the way the kernel recreates them
// after systemd disabled and vmmd re-enabled the parent's controllers.
func resetLikeSystemdReload(t *testing.T, scope string) {
	t.Helper()
	writeCgroupFile(t, scope, "memory.max", "max")
	writeCgroupFile(t, scope, "cpu.weight", "100")
	writeCgroupFile(t, scope, "cpu.max", "max 100000")
}

func assertFence(t *testing.T, scope, memoryMax, cpuWeight, cpuMax string) {
	t.Helper()
	for _, c := range []struct{ name, want string }{
		{"memory.max", memoryMax},
		{"cpu.weight", cpuWeight},
		{"cpu.max", cpuMax},
	} {
		if got := readFenceFile(t, scope, c.name); got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRepairCgroupFencesRestoresFenceAfterSystemdReload(t *testing.T) {
	l := Lease{Instance: "worker-after-reload", Plan: api.PlanScale, MemoryMaxMiB: 128}
	v, scope := fencedVM(t, l)
	wantMemory := memoryMaxValue(api.BillableRAMMB(128))
	wantCPU, err := appCPUMax(api.PlanScale, 0)
	if err != nil {
		t.Fatal(err)
	}
	wantWeight := itoa(api.PlanScale.CPUWeight())

	if rep := v.RepairCgroupFences(context.Background()); rep.Checked != 1 || rep.Repaired != 0 {
		t.Fatalf("intact fence: report = %+v, want 1 checked and nothing repaired", rep)
	}

	resetLikeSystemdReload(t, scope)
	rep := v.RepairCgroupFences(context.Background())
	if rep.Repaired != 1 || rep.Failed != 0 {
		t.Fatalf("report = %+v, want one repair", rep)
	}
	assertFence(t, scope, wantMemory, wantWeight, wantCPU)

	if rep := v.RepairCgroupFences(context.Background()); rep.Repaired != 0 {
		t.Errorf("second pass repaired %d fences, want 0", rep.Repaired)
	}
}

// A disabled controller removes its files from the scope entirely.
func TestRepairCgroupFencesRecreatesMissingFiles(t *testing.T) {
	l := Lease{Instance: "controllers-disabled", Plan: api.PlanPro, MemoryMaxMiB: 512, CPUMillicores: 500}
	v, scope := fencedVM(t, l)
	for _, name := range []string{"memory.max", "cpu.weight", "cpu.max"} {
		if err := os.Remove(filepath.Join(scope, name)); err != nil {
			t.Fatal(err)
		}
	}
	if rep := v.RepairCgroupFences(context.Background()); rep.Repaired != 1 {
		t.Fatalf("report = %+v, want one repair", rep)
	}
	assertFence(t, scope, memoryMaxValue(api.BillableRAMMB(512)), itoa(api.PlanPro.CPUWeight()), "250000 500000")
}

func TestRepairCgroupFencesFollowsRuntimeCPUChanges(t *testing.T) {
	l := Lease{Instance: "cpu-policy", Plan: api.PlanPro, MemoryMaxMiB: 512, CPUMillicores: 1000}
	v, scope := fencedVM(t, l)
	if err := v.UpdateCPULimit(context.Background(), l, 500); err != nil {
		t.Fatalf("UpdateCPULimit: %v", err)
	}
	resetLikeSystemdReload(t, scope)
	v.RepairCgroupFences(context.Background())
	if got := readFenceFile(t, scope, "cpu.max"); got != "250000 500000" {
		t.Errorf("cpu.max after repair = %q, want the runtime limit 250000 500000", got)
	}

	if err := v.restoreConfiguredCPUFence(l, nil, 1000); err != nil {
		t.Fatalf("restoreConfiguredCPUFence: %v", err)
	}
	resetLikeSystemdReload(t, scope)
	v.RepairCgroupFences(context.Background())
	if got := readFenceFile(t, scope, "cpu.max"); got != "500000 500000" {
		t.Errorf("cpu.max after repair = %q, want the configured limit 500000 500000", got)
	}
}

// A repair during a snapshot capture must keep the widened limit; restoring
// the ordinary fence there would OOM the capture.
func TestRepairCgroupFencesKeepsSnapshotHeadroom(t *testing.T) {
	l := Lease{Instance: "capturing", Plan: api.PlanScale, MemoryMaxMiB: 1024}
	v, scope := fencedVM(t, l)
	restore, err := v.widenSnapshotFence(l)
	if err != nil {
		t.Fatalf("widenSnapshotFence: %v", err)
	}
	resetLikeSystemdReload(t, scope)
	v.RepairCgroupFences(context.Background())
	if got, want := readFenceFile(t, scope, "memory.max"), memoryMaxValue(api.SnapshotMemoryMaxMB(1024)); got != want {
		t.Errorf("memory.max during capture = %q, want widened %q", got, want)
	}

	if err := restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	ordinary := memoryMaxValue(api.BillableRAMMB(1024))
	if got := readFenceFile(t, scope, "memory.max"); got != ordinary {
		t.Errorf("memory.max after capture = %q, want %q", got, ordinary)
	}
	if rep := v.RepairCgroupFences(context.Background()); rep.Repaired != 0 {
		t.Errorf("restored fence still drifted: %+v", rep)
	}
}

func TestRepairCgroupFencesRestoresBuilderFence(t *testing.T) {
	l := Lease{Instance: "builder", IsBuilder: true, MemoryMaxMiB: 2048}
	v, scope := fencedVM(t, l)
	resetLikeSystemdReload(t, scope)
	if rep := v.RepairCgroupFences(context.Background()); rep.Repaired != 1 {
		t.Fatalf("report = %+v, want one repair", rep)
	}
	assertFence(t, scope, memoryMaxValue(api.BuilderMemoryMaxMB(2048)), "256", builderCPUMax)
}

func TestRepairCgroupFencesDropsTornDownScope(t *testing.T) {
	l := Lease{Instance: "gone", Plan: api.PlanHobby, MemoryMaxMiB: 256}
	v, scope := fencedVM(t, l)
	if err := os.RemoveAll(scope); err != nil {
		t.Fatal(err)
	}
	if rep := v.RepairCgroupFences(context.Background()); rep.Gone != 1 || rep.Repaired != 0 {
		t.Fatalf("report = %+v, want the record dropped", rep)
	}
	if rep := v.RepairCgroupFences(context.Background()); rep.Checked != 0 {
		t.Errorf("record still tracked: %+v", rep)
	}
}

func TestTrackFenceValueRevertKeepsNewerChange(t *testing.T) {
	l := Lease{Instance: "revert", Plan: api.PlanPro, MemoryMaxMiB: 512}
	v, _ := fencedVM(t, l)
	revertFirst := v.trackFenceValue(l.Instance, fenceCPUMax, "1 100000")
	v.trackFenceValue(l.Instance, fenceCPUMax, "2 100000")
	revertFirst()
	if got := v.fences[l.Instance].cpuMax; got != "2 100000" {
		t.Errorf("cpuMax = %q, want the newer change kept", got)
	}
}

// adr: 20261027090014 — an agent run's networkless VM gets the whole vCPUs its plan
// sizes it with (two on Pro), while app leases keep the app CPU tiers.
func TestCgroupFenceSizesAgentRunCPU(t *testing.T) {
	period := api.PlanPro.CPUPeriodUS()
	for _, tc := range []struct {
		name  string
		lease Lease
		want  string
	}{
		{"agent run 2 vCPU", Lease{Instance: "agent-1", Plan: api.PlanPro, MemoryMaxMiB: 1024, CPUMillicores: 2000, Networkless: true},
			fmt.Sprintf("%d %d", 2*period, period)},
		{"agent run 1 vCPU", Lease{Instance: "agent-2", Plan: api.PlanHobby, MemoryMaxMiB: 1024, CPUMillicores: 1000, Networkless: true},
			fmt.Sprintf("%d %d", api.PlanHobby.CPUPeriodUS(), api.PlanHobby.CPUPeriodUS())},
		{"app 500m", Lease{Instance: "app-1", Plan: api.PlanPro, MemoryMaxMiB: 512, CPUMillicores: 500},
			fmt.Sprintf("%d %d", period/2, period)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := newCgroupFence(tc.lease)
			if err != nil || f.cpuMax != tc.want {
				t.Fatalf("cpu.max = %q, %v; want %q", f.cpuMax, err, tc.want)
			}
		})
	}
	for _, lease := range []Lease{
		{Instance: "app-2", Plan: api.PlanPro, MemoryMaxMiB: 512, CPUMillicores: 2000},
		{Instance: "agent-3", Plan: api.PlanPro, MemoryMaxMiB: 1024, CPUMillicores: 1500, Networkless: true},
		{Instance: "agent-4", Plan: api.PlanPro, MemoryMaxMiB: 1024, CPUMillicores: 3000, Networkless: true},
	} {
		if _, err := newCgroupFence(lease); err == nil {
			t.Errorf("%s with %dm: fence accepted", lease.Instance, lease.CPUMillicores)
		}
	}
}
