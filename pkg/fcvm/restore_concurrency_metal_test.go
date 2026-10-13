//go:build metal

// adr: 225 — a burst of wakes restored from one snapshot, under different
// restore concurrency limits and with the working-set prefetch on and off.
package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalRestoreBurst snapshots one 1 GiB app VM (FAAS_TEST_LAYER_ROOTFS on
// :8080), then for each setting drops the page cache and wakes
// FAAS_TEST_RESTORE_BURST_VMS VMs (default 12) from that snapshot at once.
// It reports each wake's time to a served first request and the burst's
// wall time.
func TestMetalRestoreBurst(t *testing.T) {
	kernel, base, layer := metalImages(t)
	if os.Getenv("FAAS_TEST_RESTORE_BURST") != "1" {
		t.Skip("set FAAS_TEST_RESTORE_BURST=1 (wakes 12 1 GiB VMs at once)")
	}
	n := 12
	if raw := os.Getenv("FAAS_TEST_RESTORE_BURST_VMS"); raw != "" {
		var err error
		if n, err = strconv.Atoi(raw); err != nil || n < 1 {
			t.Fatalf("FAAS_TEST_RESTORE_BURST_VMS=%q", raw)
		}
	}
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
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
	withCgroupRootAt(t, "/sys/fs/cgroup")
	snap := &Snapshot{FCVersion: fcVersion, StorageKey: "snap/burst/mem", VMStateStorageKey: "snap/burst/vmstate", VMStatePath: t.TempDir() + "/vmstate"}
	wakeReq := func(id string, s *Snapshot) WakeRequest {
		return WakeRequest{Instance: id, AccountID: "acct", AppID: "app-burst", Plan: "scale",
			BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024, Snapshot: s}
	}
	src := NewManager(wire.ExecRunner{}, newMetalVMM(t, 180*time.Second).WithStorage(store), Paths{Kernel: kernel}, fcVersion, nil, nil)
	inst, err := src.Wake(ctx, wakeReq("burst-src", nil))
	if err != nil {
		t.Fatalf("wake snapshot source: %v", err)
	}
	h := "http://" + inst.Lease.HostIP.String() + ":8080"
	if _, err := firstResponse(h+"/", 120*time.Second); err != nil {
		t.Fatalf("snapshot source does not serve: %v", err)
	}
	if _, err := firstResponse(h+"/heavy", 60*time.Second); err != nil {
		t.Fatalf("snapshot source /heavy: %v", err)
	}
	if _, err := src.Park(ctx, "burst-src", SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
		t.Fatalf("park snapshot source: %v", err)
	}

	for _, setting := range []struct {
		name        string
		concurrency int
		prefetch    bool
	}{
		{"concurrency 1, prefetch", 1, true},
		{"concurrency 3, prefetch", 3, true},
		{"concurrency 3, no prefetch", 3, false},
		{"concurrency 6, prefetch", 6, true},
		{"unlimited, prefetch", 0, true},
	} {
		v := newMetalVMM(t, 180*time.Second).WithStorage(store).WithRestoreConcurrency(setting.concurrency).WithRestorePrefetch(setting.prefetch)
		m := NewManager(wire.ExecRunner{}, v, Paths{Kernel: kernel}, fcVersion, nil, nil)
		// Record the snapshot's working set so prefetch has one to use.
		prime := fmt.Sprintf("burst-prime-%d-%t", setting.concurrency, setting.prefetch)
		if _, err := m.Wake(ctx, wakeReq(prime, snap)); err != nil {
			t.Fatalf("%s: prime wake: %v", setting.name, err)
		}
		time.Sleep(2 * time.Second)
		_ = m.Destroy(ctx, prime)
		dropPageCache(t)

		lat := make([]time.Duration, n)
		methods := make([]WakeMethod, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		start := time.Now()
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id := fmt.Sprintf("burst-%d-%t-%d", setting.concurrency, setting.prefetch, i)
				t0 := time.Now()
				inst, err := m.Wake(ctx, wakeReq(id, snap))
				if err != nil {
					errs[i] = err
					return
				}
				methods[i] = inst.Method
				if _, err := firstResponse("http://"+inst.Lease.HostIP.String()+":8080/", 60*time.Second); err != nil {
					errs[i] = err
					return
				}
				lat[i] = time.Since(t0)
			}(i)
		}
		wg.Wait()
		wall := time.Since(start)
		for i := 0; i < n; i++ {
			_ = m.Destroy(ctx, fmt.Sprintf("burst-%d-%t-%d", setting.concurrency, setting.prefetch, i))
			if errs[i] != nil {
				t.Fatalf("%s: wake %d: %v", setting.name, i, errs[i])
			}
			if methods[i] != WakeRestore {
				t.Fatalf("%s: wake %d came back by %s", setting.name, i, methods[i])
			}
		}
		sorted := append([]time.Duration(nil), lat...)
		sort.Slice(sorted, func(a, b int) bool { return sorted[a] < sorted[b] })
		t.Logf("restore burst, %s: %d wakes, first byte p50 %v p95 %v max %v, burst wall %v",
			setting.name, n, burstPercentile(sorted, 50), burstPercentile(sorted, 95), burstPercentile(sorted, 100), wall.Round(time.Millisecond))
	}
}

// burstPercentile is the p-th percentile of sorted durations.
func burstPercentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := (len(sorted)*p + 99) / 100
	if i < 1 {
		i = 1
	}
	return sorted[i-1].Round(time.Millisecond)
}
