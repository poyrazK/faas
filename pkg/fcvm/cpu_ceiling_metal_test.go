//go:build metal

// adr: 20261109172421

package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalAppCPUFollowsSize pins ADR-20261109172421 steps 2 and 6.3 on real
// Firecracker and cgroupfs. An app VM runs without a CPU quota whatever its
// plan, with its size's cpu.weight and the vCPUs it was booted with, through
// cold boot, park and restore. A live CPU policy of 500 millicores caps it
// and 1000 or more lifts the cap again. It replaces the ADR-20261101160231
// step 4 test, whose Hobby two-core refusal and two-core quota went with
// step 6.3 (#6935): no vmmd CPU value reads the plan.
func TestMetalAppCPUFollowsSize(t *testing.T) {
	kernel, base, layer := metalImages(t)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	v := newMetalVMM(t, 30*time.Second).WithStorage(store)
	m := withMetalFlatTenantCgroups(NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil))
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const (
		vcpus    = 2
		memMiB   = 128
		uncapped = "max 100000"
	)
	wantWeight := strconv.Itoa(api.SizeCPUWeight(memMiB))
	check := func(stage string, plan api.Plan, instance, wantCPUMax string) {
		t.Helper()
		scope := filepath.Join(cgroupRoot, tenantCgroupParent(Lease{Instance: instance, Plan: plan}), PerInstanceScope(instance))
		read := func(name string) string {
			t.Helper()
			raw, err := os.ReadFile(filepath.Join(scope, name))
			if err != nil {
				t.Fatalf("%s: read %s: %v", stage, name, err)
			}
			return strings.TrimSpace(string(raw))
		}
		if got := read("cpu.max"); got != wantCPUMax {
			t.Fatalf("%s: cpu.max = %q, want %q", stage, got, wantCPUMax)
		}
		if got := read("cpu.weight"); got != wantWeight {
			t.Fatalf("%s: cpu.weight = %s, want %s (the size's weight)", stage, got, wantWeight)
		}
		pid, ok := v.InstancePID(instance)
		if !ok {
			t.Fatalf("%s: no firecracker pid", stage)
		}
		n, err := vcpuThreadCount("/proc", pid)
		if err != nil {
			t.Fatalf("%s: count vCPU threads: %v", stage, err)
		}
		if n != vcpus {
			t.Fatalf("%s: %d vCPU threads, want %d", stage, n, vcpus)
		}
		t.Logf("%s: cpu.max %q, cpu.weight %s, %d vCPUs", stage, wantCPUMax, wantWeight, n)
	}

	// The plan bounds nothing: a Hobby VM of the same size runs the same
	// machine as a Scale one.
	const hobby = "cpu-size-hobby"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), hobby) })
	if _, err := m.ColdBoot(ctx, ColdBootRequest{AppID: "app-" + hobby, Instance: hobby, Plan: api.PlanHobby,
		BaseKey: base, LayerKey: layer, VcpuCount: vcpus, MemSizeMiB: memMiB}); err != nil {
		t.Fatalf("Hobby cold boot: %v", err)
	}
	check("Hobby cold boot", api.PlanHobby, hobby, uncapped)
	if err := m.Destroy(ctx, hobby); err != nil {
		t.Fatalf("destroy Hobby: %v", err)
	}

	const instance = "cpu-size-scale"
	const appID = "app-" + instance
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if _, err := m.ColdBoot(ctx, ColdBootRequest{AppID: appID, Instance: instance, Plan: api.PlanScale,
		BaseKey: base, LayerKey: layer, VcpuCount: vcpus, MemSizeMiB: memMiB, CPUMillicores: 2000}); err != nil {
		t.Fatalf("Scale cold boot at 2000 m: %v", err)
	}
	check("Scale cold boot at 2000 m", api.PlanScale, instance, uncapped)

	snap := &Snapshot{
		FCVersion: fcVersion, StorageKey: "snap/cpu-size/mem", VMStateStorageKey: "snap/cpu-size/vmstate",
		VMStatePath: filepath.Join(t.TempDir(), "vmstate"),
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	out, err := m.Wake(ctx, WakeRequest{AppID: appID, Instance: instance, Plan: api.PlanScale,
		BaseKey: base, LayerKey: layer, VcpuCount: vcpus, MemSizeMiB: memMiB, CPUMillicores: 2000, Snapshot: snap})
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if out.Method != WakeRestore {
		t.Fatalf("wake = %s, want restore", out.Method)
	}
	check("restore", api.PlanScale, instance, uncapped)

	// A 250 or 500 millicore choice stays a cap (ADR-152), also across live
	// policy updates; the default and above run uncapped.
	for i, step := range []struct {
		millicores int
		cpuMax     string
	}{
		{500, "50000 100000"},
		{1000, uncapped},
		{2000, uncapped},
	} {
		if err := m.UpdateAppCPULimit(ctx, appID, int64(i+1), step.millicores); err != nil {
			t.Fatalf("live policy to %d m: %v", step.millicores, err)
		}
		check("policy "+strconv.Itoa(step.millicores)+" m", api.PlanScale, instance, step.cpuMax)
	}

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}
