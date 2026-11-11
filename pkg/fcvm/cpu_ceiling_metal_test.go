//go:build metal

// adr: 20261101160231

package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalScaleTwoCoreCeiling pins ADR-20261101160231 step 4 on real
// Firecracker and cgroupfs: a Scale VM boots, parks and restores with two
// vCPUs under a two-core cpu.max, the live CPU policy moves it between one and
// two cores, and a plan whose ceiling is one core refuses two.
func TestMetalScaleTwoCoreCeiling(t *testing.T) {
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
	m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// A Hobby app's ceiling is one core: two are refused before any VM runs.
	const refused = "cpu-ceiling-hobby"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), refused) })
	_, err = m.ColdBoot(ctx, ColdBootRequest{AppID: "app-" + refused, Instance: refused, Plan: api.PlanHobby,
		BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 2000})
	if err == nil || !strings.Contains(err.Error(), "cpu_millicores") {
		t.Fatalf("Hobby cold boot at 2000 m: err = %v, want a cpu_millicores refusal", err)
	}
	t.Logf("Hobby at 2000 m refused: %v", err)
	_ = m.Destroy(ctx, refused)

	const instance = "cpu-ceiling-scale"
	const appID = "app-" + instance
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	scope := filepath.Join(cgroupRoot, ParentCgroupFor(api.PlanScale), PerInstanceScope(instance))
	period := cpuMaxPeriodUS
	check := func(stage string, wantMillicores int) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(scope, "cpu.max"))
		if err != nil {
			t.Fatalf("%s: read cpu.max: %v", stage, err)
		}
		want := itoa(wantMillicores*period/1000) + " " + itoa(period)
		if got := strings.TrimSpace(string(raw)); got != want {
			t.Fatalf("%s: cpu.max = %q, want %q", stage, got, want)
		}
		pid, ok := v.InstancePID(instance)
		if !ok {
			t.Fatalf("%s: no firecracker pid", stage)
		}
		n, err := vcpuThreadCount("/proc", pid)
		if err != nil {
			t.Fatalf("%s: count vCPU threads: %v", stage, err)
		}
		if n != 2 {
			t.Fatalf("%s: %d vCPU threads, want 2", stage, n)
		}
		t.Logf("%s: cpu.max %q, %d vCPUs", stage, want, n)
	}

	if _, err := m.ColdBoot(ctx, ColdBootRequest{AppID: appID, Instance: instance, Plan: api.PlanScale,
		BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 2000}); err != nil {
		t.Fatalf("Scale cold boot at 2000 m: %v", err)
	}
	check("cold boot", 2000)

	snap := &Snapshot{
		FCVersion: fcVersion, StorageKey: "snap/cpu-ceiling/mem", VMStateStorageKey: "snap/cpu-ceiling/vmstate",
		VMStatePath: filepath.Join(t.TempDir(), "vmstate"),
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	out, err := m.Wake(ctx, WakeRequest{AppID: appID, Instance: instance, Plan: api.PlanScale,
		BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 2000, Snapshot: snap})
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if out.Method != WakeRestore {
		t.Fatalf("wake = %s, want restore", out.Method)
	}
	check("restore", 2000)

	if err := m.UpdateAppCPULimit(ctx, appID, 1, 1000); err != nil {
		t.Fatalf("live policy to 1000 m: %v", err)
	}
	check("policy 1000 m", 1000)
	if err := m.UpdateAppCPULimit(ctx, appID, 2, 2000); err != nil {
		t.Fatalf("live policy to 2000 m: %v", err)
	}
	check("policy 2000 m", 2000)

	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}
