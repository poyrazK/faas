//go:build linux && metal

package fcvm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Runs concurrent restores with guest-init images, an explicit vmmd helper, and
// private network/mount namespaces. Unlike legacy fixtures, keyed snapshots
// use real storage, requests carry a plan, and the helper is not a test binary.
// This verifies physical restore isolation; it is not the full gateway wake SLO.
func TestMetalInitialBurstConcurrentRestores(t *testing.T) {
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
	for cycle := 0; cycle < 20; cycle++ {
		var instances [2]*Instance
		var failures [2]error
		var times [2]float64
		var wg sync.WaitGroup
		for i, name := range []string{"opta", "optb"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				start := time.Now()
				instances[i], failures[i] = m.Wake(ctx, WakeRequest{Instance: name, BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: 128, Plan: "hobby", Snapshot: snap})
				times[i] = float64(time.Since(start)) / float64(time.Millisecond)
			}()
		}
		wg.Wait()
		for i, inst := range instances {
			if failures[i] != nil {
				t.Fatalf("cycle %d restore %d: %v", cycle, i, failures[i])
			}
			if inst.Method != WakeRestore {
				t.Fatalf("cycle %d restore %d fell back to cold boot: %s", cycle, i, inst.RestoreError)
			}
			latencies = append(latencies, times[i])
			uuid := fetchV6UUID(t, inst.Lease.HostIP.String())
			if uuid == "" || seenUUIDs[uuid] {
				t.Fatalf("cycle %d restore %d: empty or duplicate guest UUID", cycle, i)
			}
			seenUUIDs[uuid] = true
		}
		a, b := instances[0].Lease, instances[1].Lease
		if a.UID == b.UID || a.Netns == b.Netns || a.HostIP == b.HostIP {
			t.Fatal("concurrent restores share a lease identity")
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
