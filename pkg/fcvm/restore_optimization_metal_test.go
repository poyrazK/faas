//go:build linux && metal

package fcvm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Runs with freshly built guest-init images, an explicit vmmd helper, and
// private network/mount namespaces. Unlike legacy fixtures, keyed snapshots
// use real storage, requests carry a plan, and the helper is not a test binary.
// This measures Manager.Wake, not the gateway's full request-wake SLO.
func TestMetalOptimizedRestore(t *testing.T) {
	helper := os.Getenv("FAAS_TEST_VMMD_HELPER")
	if helper == "" {
		t.Skip("set FAAS_TEST_VMMD_HELPER with isolated network, jail and current guest fixtures")
	}
	kernel, base, layer := metalImages(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	for _, argv := range [][]string{
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	store, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vmm := NewJailerVMM(JailChrootBase, 30*time.Second).WithStorage(store)
	vmm.mountHelperPath = helper
	m := NewManager(run, vmm, Paths{Kernel: kernel}, version, nil, nil)
	// Reserve the top two test-only UIDs, away from normal low-slot allocation.
	// The runner verifies these UIDs are unused before invoking this test.
	m.alloc.free = []int{MaxSlots - 1, MaxSlots - 2}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	for _, name := range []string{"optprime", "opta", "optb"} {
		t.Cleanup(func() { _, _, _ = m.SignalAndKill(context.Background(), name, 0, 0) })
	}
	_, err = m.ColdBoot(ctx, ColdBootRequest{Instance: "optprime", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby"})
	if err != nil {
		t.Fatal(err)
	}
	snap := &Snapshot{FCVersion: version, StorageKey: "snap/opt/mem", VMStateStorageKey: "snap/opt/vmstate", VMStatePath: filepath.Join(t.TempDir(), "vmstate")}
	if _, err := m.Park(ctx, "optprime", SnapshotSpec{StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey, VMStatePath: snap.VMStatePath}); err != nil {
		t.Fatal(err)
	}
	seenUUIDs := make(map[string]bool)
	var latencies []float64
	for cycle := 0; cycle < 50; cycle++ {
		var first *Instance
		for _, name := range []string{"opta", "optb"} {
			start := time.Now()
			inst, err := m.Wake(ctx, WakeRequest{Instance: name, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby", Snapshot: snap})
			latencies = append(latencies, float64(time.Since(start))/float64(time.Millisecond))
			if err != nil {
				t.Fatalf("cycle %d %s: %v", cycle, name, err)
			}
			if inst.Method != WakeRestore {
				t.Fatalf("cycle %d fell back to cold boot: %s", cycle, inst.RestoreError)
			}
			if first != nil && (first.Lease.UID == inst.Lease.UID || first.Lease.Netns == inst.Lease.Netns || first.Lease.HostIP == inst.Lease.HostIP) {
				t.Fatal("two live restores share a lease identity")
			}
			first = inst
			uuid := fetchV6UUID(t, inst.Lease.HostIP.String())
			if uuid == "" || seenUUIDs[uuid] {
				t.Fatalf("cycle %d %s: empty UUID=%t duplicate=%t", cycle, name, uuid == "", seenUUIDs[uuid])
			}
			seenUUIDs[uuid] = true
		}
		for _, name := range []string{"opta", "optb"} {
			if _, _, err := m.SignalAndKill(ctx, name, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("leaked live=%d leases=%d", m.LiveCount(), m.LeasedCount())
	}
	data, err := json.Marshal(latencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("manager_wake_samples_ms=%s", data)
}
