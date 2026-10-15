//go:build metal

// adr: 906 — frozen instances at density: freezing idle VMs returns most of
// their memory to the host, and each thaws to its first byte far faster than
// a snapshot restore.
package fcvm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalFreezeDensity boots FAAS_TEST_FREEZE_DENSITY_VMS 1 GiB app VMs
// (default 12; FAAS_TEST_LAYER_ROOTFS starts an app on :8080 that answers
// /heavy), freezes them all, then thaws each and times its first byte and a
// CPU-bound request. With swap (zram or zswap) on the host, a freeze also
// moves anonymous memory out. FAAS_TEST_FREEZE_FROM_SNAPSHOT=1 restores every
// VM from one snapshot first, as production wakes do: pages read since the
// restore stay file-backed and reclaimable without swap.
// FAAS_TEST_FREEZE_TRAFFIC (a duration) serves every VM a request mix for
// that long before the freeze, so a restored VM writes into memory of its
// own the way a serving one does (prod: 48-66 MB anonymous, ADR-906).
func TestMetalFreezeDensity(t *testing.T) {
	kernel, base, layer := metalImages(t)
	if os.Getenv("FAAS_TEST_FREEZE_DENSITY") != "1" {
		t.Skip("set FAAS_TEST_FREEZE_DENSITY=1 (boots 12 1 GiB VMs)")
	}
	n := 12
	if raw := os.Getenv("FAAS_TEST_FREEZE_DENSITY_VMS"); raw != "" {
		var err error
		if n, err = strconv.Atoi(raw); err != nil || n < 1 {
			t.Fatalf("FAAS_TEST_FREEZE_DENSITY_VMS=%q", raw)
		}
	}
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Skipf("firecracker version: %v", err)
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 180*time.Second).WithStorage(store), Paths{Kernel: kernel}, fcVersion, nil, nil)
	var snap *Snapshot
	if os.Getenv("FAAS_TEST_FREEZE_FROM_SNAPSHOT") == "1" {
		snap = &Snapshot{FCVersion: fcVersion, StorageKey: "snap/frz/mem", VMStateStorageKey: "snap/frz/vmstate", VMStatePath: t.TempDir() + "/vmstate"}
		src, err := m.Wake(ctx, WakeRequest{Instance: "frz-src", AccountID: "acct", AppID: "app-frz", Plan: "scale",
			BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024})
		if err != nil {
			t.Fatalf("wake snapshot source: %v", err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), "frz-src") })
		if _, err := firstResponse("http://"+src.Lease.HostIP.String()+":8080/", 120*time.Second); err != nil {
			t.Fatalf("snapshot source does not serve: %v", err)
		}
		get(t, "http://"+src.Lease.HostIP.String()+":8080/heavy")
		if _, err := m.Park(ctx, "frz-src", SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}); err != nil {
			t.Fatalf("park snapshot source: %v", err)
		}
	}
	ids := make([]string, n)
	hosts := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("frz-%d", i)
		inst, err := m.Wake(ctx, WakeRequest{Instance: ids[i], AccountID: "acct", AppID: "app-frz", Plan: "scale",
			BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024, Snapshot: snap})
		if err != nil {
			t.Fatalf("wake %d: %v", i, err)
		}
		if snap != nil && inst.Method != WakeRestore {
			t.Fatalf("wake %d came back by %s, want restore", i, inst.Method)
		}
		id := ids[i]
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		hosts[i] = "http://" + inst.Lease.HostIP.String() + ":8080"
		if _, err := firstResponse(hosts[i]+"/", 120*time.Second); err != nil {
			t.Fatalf("%s does not serve: %v", id, err)
		}
		get(t, hosts[i]+"/heavy")
	}
	if raw := os.Getenv("FAAS_TEST_FREEZE_TRAFFIC"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("FAAS_TEST_FREEZE_TRAFFIC=%q: %v", raw, err)
		}
		serveTraffic(t, hosts, d)
	}
	time.Sleep(3 * time.Second)
	vmMB := func() (total int64) {
		for _, id := range ids {
			if cur, err := readCgroupInt(cgroupRoot+"/"+ParentCgroupFor("scale")+"/"+PerInstanceScope(id), "memory.current"); err == nil {
				total += cur >> 20
			}
		}
		return total
	}
	vmStatMB := func(key string) (total int64) {
		for _, id := range ids {
			if stat, err := readCgroupStat(cgroupRoot + "/" + ParentCgroupFor("scale") + "/" + PerInstanceScope(id)); err == nil {
				total += stat[key] >> 20
			}
		}
		return total
	}
	running, availRunning := vmMB(), memAvailableMB(t)
	t.Logf("running: per VM %.1f MB anonymous, %.1f MB file", float64(vmStatMB("anon"))/float64(n), float64(vmStatMB("file"))/float64(n))

	var freezeTimes []time.Duration
	var swapped int64
	var freedMB []int64
	kept := 0
	for _, id := range ids {
		start := time.Now()
		rep, err := m.Freeze(ctx, id, FreezeOptions{Reclaim: true})
		if err != nil {
			t.Fatalf("freeze %s: %v", id, err)
		}
		freezeTimes = append(freezeTimes, time.Since(start))
		swapped += rep.SwappedBytes >> 20
		// schedd keeps a compressed VM frozen only when this frees at least
		// 16 MB and half its memory (pkg/sched/frozen_compress.go).
		freed := rep.BeforeBytes - rep.AfterBytes
		freedMB = append(freedMB, freed>>20)
		if rep.BeforeBytes > 0 && freed >= 16<<20 && freed*2 >= rep.BeforeBytes {
			kept++
		}
	}
	time.Sleep(2 * time.Second)
	frozen, availFrozen := vmMB(), memAvailableMB(t)
	t.Logf("freeze: %d VMs, VM cgroups %d -> %d MB (%.1f -> %.1f per VM), %d MB to swap, host MemAvailable %d -> %d MB, freeze p50 %v max %v",
		n, running, frozen, float64(running)/float64(n), float64(frozen)/float64(n), swapped, availRunning, availFrozen,
		percentile(freezeTimes, 50), percentile(freezeTimes, 100))
	// zswap (ADR-906 decision 6): the RAM the compressed pool takes and the
	// memory it holds; both zero without zswap.
	t.Logf("zswap pool: %d MB holding %d MB", meminfoMB(t, "Zswap:"), meminfoMB(t, "Zswapped:"))
	sort.Slice(freedMB, func(i, j int) bool { return freedMB[i] < freedMB[j] })
	t.Logf("freed per VM median %d MB, min %d MB; %d of %d would stay compressed under schedd's rule",
		freedMB[n/2], freedMB[0], kept, n)

	var firstByte, heavy []time.Duration
	for i, id := range ids {
		start := time.Now()
		if err := m.Thaw(ctx, id); err != nil {
			t.Fatalf("thaw %s: %v", id, err)
		}
		if _, err := firstResponse(hosts[i]+"/", 30*time.Second); err != nil {
			t.Fatalf("%s does not serve after thaw: %v", id, err)
		}
		firstByte = append(firstByte, time.Since(start))
		start = time.Now()
		get(t, hosts[i]+"/heavy")
		heavy = append(heavy, time.Since(start))
	}
	t.Logf("thaw: first byte p50 %v p99 %v max %v; CPU-bound request p50 %v max %v; VM cgroups %d MB",
		percentile(firstByte, 50), percentile(firstByte, 99), percentile(firstByte, 100),
		percentile(heavy, 50), percentile(heavy, 100), vmMB())
	if snap != nil {
		// What a request would pay without the frozen tier: a snapshot
		// restore of the same app, to its first byte and a CPU-bound request.
		var restoreFirst, restoreHeavy []time.Duration
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("frz-ref-%d", i)
			start := time.Now()
			inst, err := m.Wake(ctx, WakeRequest{Instance: id, AccountID: "acct", AppID: "app-frz", Plan: "scale",
				BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024, Snapshot: snap})
			if err != nil {
				t.Fatalf("reference restore %d: %v", i, err)
			}
			host := "http://" + inst.Lease.HostIP.String() + ":8080"
			if _, err := firstResponse(host+"/", 30*time.Second); err != nil {
				t.Fatalf("reference restore %d does not serve: %v", i, err)
			}
			restoreFirst = append(restoreFirst, time.Since(start))
			start = time.Now()
			get(t, host+"/heavy")
			restoreHeavy = append(restoreHeavy, time.Since(start))
			_ = m.Destroy(context.Background(), id)
		}
		t.Logf("reference snapshot restore: first byte p50 %v max %v; CPU-bound request p50 %v max %v",
			percentile(restoreFirst, 50), percentile(restoreFirst, 100), percentile(restoreHeavy, 50), percentile(restoreHeavy, 100))
	}
	if frozen > running {
		t.Fatalf("freezing grew the VMs: %d -> %d MB", running, frozen)
	}
}

// serveTraffic serves every VM, four requests at a time, for d: JSON reads
// (allocation churn, so young-generation GC) with a CPU-bound request every
// 50th.
func serveTraffic(t *testing.T, hosts []string, d time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 60 * time.Second}
	deadline := time.Now().Add(d)
	var wg sync.WaitGroup
	var served, failed atomic.Int64
	for _, host := range hosts {
		for w := 0; w < 4; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; time.Now().Before(deadline); i++ {
					path := "/"
					if i%50 == 49 {
						path = "/heavy"
					}
					resp, err := client.Get(host + path)
					if err != nil {
						failed.Add(1)
						time.Sleep(100 * time.Millisecond)
						continue
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
					served.Add(1)
				}
			}()
		}
	}
	wg.Wait()
	t.Logf("traffic: %d requests to %d VMs over %v", served.Load(), len(hosts), d)
	if failed.Load() > 0 {
		t.Fatalf("traffic: %d requests failed", failed.Load())
	}
}

// meminfoMB reads one /proc/meminfo field in MB, or 0 when it is absent.
func meminfoMB(t *testing.T, key string) int64 {
	t.Helper()
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == key {
			kb, _ := strconv.ParseInt(f[1], 10, 64)
			return kb >> 10
		}
	}
	return 0
}

func get(t *testing.T, url string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, resp.Status)
	}
}

func percentile(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := (len(s)*p + 99) / 100
	if i < 1 {
		i = 1
	}
	return s[i-1].Round(100 * time.Microsecond)
}
