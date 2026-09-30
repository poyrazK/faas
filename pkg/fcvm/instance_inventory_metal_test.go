//go:build metal

// adr: 381 — SIGKILL must remove a VM from inventory even if its exit relay is lost.
package fcvm

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

func TestMetalInstanceInventoryAfterProcessKill(t *testing.T) {
	kernel, base, layer := metalImages(t)
	manager := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	// Simulate a lost liveness relay. Inventory must remain authoritative while
	// the Manager keeps the dead instance's entry for scheduler-owned cleanup.
	manager.WithLivenessSink(func(context.Context, string, string) {})
	vmm := manager.vmm.(*JailerVMM)
	vmm.WithProcessExitSink(manager.ProcessExited)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	const instance = "inventory-killed-vm"
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = manager.Destroy(cleanup, instance)
		leakcheck.AssertZero(t)
	})
	if _, err := manager.ColdBoot(ctx, ColdBootRequest{Instance: instance, Plan: "hobby", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128}); err != nil {
		t.Fatal(err)
	}
	ids, complete := manager.InstanceInventory()
	if !complete || len(ids) != 1 || ids[0] != instance {
		t.Fatalf("live inventory=%v/%v", ids, complete)
	}
	vmm.mu.Lock()
	cmd := vmm.proc[instance]
	vmm.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		t.Fatal("no Firecracker process to kill")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ids, complete = manager.InstanceInventory()
		if complete && len(ids) == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !complete || len(ids) != 0 {
		t.Fatalf("killed VM remains in inventory: %v", ids)
	}
	if manager.LiveCount() != 1 {
		t.Fatal("lost-relay fixture did not retain cleanup entry")
	}
	if err := manager.Destroy(ctx, instance); err != nil {
		t.Fatal(err)
	}
	if manager.LiveCount() != 0 || manager.LeasedCount() != 0 {
		t.Fatal("scheduler cleanup leaked Manager or allocator state")
	}
}
