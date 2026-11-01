//go:build metal

// adr: 20261101160231

package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalRestoreRefusesOtherVCPUCount pins the ADR-20261101160231 guard on
// real Firecracker: a snapshot captured with two vCPUs, woken for one, is
// refused after /snapshot/load and the wake cold-boots with one vCPU. The
// next capture then restores at one vCPU.
func TestMetalRestoreRefusesOtherVCPUCount(t *testing.T) {
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
	const instance = "vcpu-guard"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	snap := &Snapshot{
		FCVersion: fcVersion, StorageKey: "snap/vcpu-guard/mem", VMStateStorageKey: "snap/vcpu-guard/vmstate",
		VMStatePath: filepath.Join(t.TempDir(), "vmstate"),
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	vcpus := func() int {
		t.Helper()
		pid, ok := v.InstancePID(instance)
		if !ok {
			t.Fatal("no firecracker pid for the instance")
		}
		n, err := vcpuThreadCount("/proc", pid)
		if err != nil {
			t.Fatalf("count vCPU threads: %v", err)
		}
		return n
	}
	wake := func(count int) WakeMethod {
		t.Helper()
		out, err := m.Wake(ctx, WakeRequest{Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: count, MemSizeMiB: 128, Snapshot: snap})
		if err != nil {
			t.Fatalf("wake with %d vCPUs: %v", count, err)
		}
		if got := vcpus(); got != count {
			t.Fatalf("woke (%s) with %d vCPU threads, want %d", out.Method, got, count)
		}
		return out.Method
	}

	if _, err := m.ColdBoot(ctx, ColdBootRequest{Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 128}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park the two-vCPU capture: %v", err)
	}
	if method := wake(2); method != WakeRestore {
		t.Fatalf("same-count wake = %s, want restore", method)
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park again: %v", err)
	}
	if method := wake(1); method != WakeColdBoot {
		t.Fatalf("one-vCPU wake of a two-vCPU snapshot = %s, want the cold-boot fallback", method)
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("park the one-vCPU capture: %v", err)
	}
	if method := wake(1); method != WakeRestore {
		t.Fatalf("one-vCPU wake of a one-vCPU snapshot = %s, want restore", method)
	}
	if err := m.Destroy(ctx, instance); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}
