//go:build metal && linux

package fcvm

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"
	"unsafe"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
	"golang.org/x/sys/unix"
)

type prefetchMetalRig struct {
	m         *Manager
	vmm       *JailerVMM
	base      string
	layer     string
	mem       int
	snap      *Snapshot
	spec      SnapshotSpec
	memPath   string
	statePath string
	health    string
	prepared  bool
}

// newPrefetchMetalRig cold-boots one guest and parks it under a production v2
// capture key (which also captures the writable drive). base/layer default to
// the self-contained v6 fixture.
func newPrefetchMetalRig(t *testing.T, base, layer string, memMiB int, health string, prepared bool) *prefetchMetalRig {
	t.Helper()
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v", kernel, err)
	}
	dir := benchFixtureDir(t)
	if base == "" {
		base, layer = filepath.Join(dir, "base.ext4"), filepath.Join(dir, "layer.ext4")
		if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
			t.Fatal(err)
		}
		if err := buildV6LayerExt4Size(layer, 64); err != nil {
			t.Fatal(err)
		}
	} else {
		// The layer is reflink-cloned next to its source; keep a private copy
		// on the reflink volume so the caller's image is never modified.
		private := filepath.Join(dir, "layer.ext4")
		if err := copyFile(layer, private); err != nil {
			t.Fatal(err)
		}
		layer = private
	}
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(root)
	if err != nil {
		t.Fatal(err)
	}
	vmm := newMetalVMM(t, 60*time.Second).WithStorage(store)
	stageBenchMountHelper(t, vmm)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	key := func(leaf string) string { return "snap/prefetch-metal/captures/c1/v2/" + leaf }
	r := &prefetchMetalRig{
		m:   NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil),
		vmm: vmm, base: base, layer: layer, mem: memMiB, health: health, prepared: prepared,
		memPath: filepath.Join(root, key("mem")), statePath: filepath.Join(root, key("vmstate")),
	}
	r.snap = &Snapshot{FCVersion: fcVersion, StorageKey: key("mem"), VMStateStorageKey: key("vmstate"),
		VMStatePath: filepath.Join(t.TempDir(), "vmstate")}
	r.spec = SnapshotSpec{VMStatePath: r.snap.VMStatePath, StorageKey: r.snap.StorageKey, VMStateStorageKey: r.snap.VMStateStorageKey}
	t.Cleanup(func() { _ = r.m.Destroy(context.Background(), "prefetch-metal") })
	ctx := context.Background()
	if prepared {
		if err := r.m.EnablePreparedNetworks(ctx, 3); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = r.m.ClosePreparedNetworks() })
	}
	cold := ColdBootRequest{Instance: "prefetch-metal", Plan: "pro", BaseKey: base, LayerKey: layer,
		VcpuCount: 2, MemSizeMiB: memMiB, HealthcheckPath: health, StartupDeadlineS: 60}
	if prepared {
		cold.Plan, cold.Port, cold.EgressMbit = "scale", netns.AppPort, 250
	}
	if _, err := r.m.ColdBoot(ctx, cold); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	if _, err := r.m.Park(ctx, "prefetch-metal", r.spec); err != nil {
		t.Fatalf("prime park: %v", err)
	}
	return r
}

// evict drops the snapshot's mem and vmstate pages from the page cache — the
// state a production node leaves a snapshot in once later parks evict it.
func (r *prefetchMetalRig) evict() {
	for _, p := range []string{r.memPath, r.statePath} {
		if f, err := os.OpenFile(p, os.O_RDWR, 0); err == nil {
			_ = f.Sync()
			_ = unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
			_ = f.Close()
		}
	}
}

func (r *prefetchMetalRig) wake(t *testing.T) time.Duration {
	t.Helper()
	return r.wakeWith(t, nil)
}

// wakeWith is wake with a hook to adjust the request (e.g. API env).
func (r *prefetchMetalRig) wakeWith(t *testing.T, mutate func(*WakeRequest)) time.Duration {
	t.Helper()
	req := WakeRequest{Instance: "prefetch-metal", Plan: "pro", BaseKey: r.base,
		LayerKey: r.layer, VcpuCount: 2, MemSizeMiB: r.mem, Snapshot: r.snap, HealthcheckPath: r.health, StartupDeadlineS: 60}
	if mutate != nil {
		mutate(&req)
	}
	if r.prepared {
		req.Plan, req.Port, req.EgressMbit = "scale", netns.AppPort, 250
		waitPreparedBenchmarkNetwork(t, r.m, context.Background())
	}
	start := time.Now()
	out, err := r.m.Wake(context.Background(), req)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if out.Method != WakeRestore {
		t.Fatalf("wake method = %s, want restore", out.Method)
	}
	if r.prepared && out.NetnsTapMs > 5 {
		t.Fatalf("wake built its network inline (%d ms); the prepared pool missed", out.NetnsTapMs)
	}
	return time.Since(start)
}

// waitRecorded waits for the background working-set record of the last wake.
func (r *prefetchMetalRig) waitRecorded(t *testing.T) restorePrefetchSet {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if set, ok := r.vmm.restorePrefetch.get(snapshotPrefetchFamily(r.snap.StorageKey)); ok {
			return set
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("restore working set was never recorded")
	return restorePrefetchSet{}
}

func residentBytes(t *testing.T, path string, ranges []fileRange) int64 {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	b, err := unix.Mmap(int(f.Fd()), 0, int(fi.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Munmap(b) }()
	page := int64(os.Getpagesize())
	var resident int64
	for _, rg := range ranges {
		vec := make([]byte, (rg.Len+page-1)/page)
		region := b[rg.Off : rg.Off+rg.Len]
		if _, _, errno := unix.Syscall(unix.SYS_MINCORE, uintptr(unsafe.Pointer(&region[0])), uintptr(len(region)), uintptr(unsafe.Pointer(&vec[0]))); errno != 0 {
			t.Fatal(errno)
		}
		for _, v := range vec {
			if v&1 == 1 {
				resident += page
			}
		}
	}
	return resident
}

// adr: 224 — a restore records the guest's working set from Firecracker's
// page table, and a later prefetch of that family brings those exact pages
// back into the page cache without the guest running.
func TestMetalRestorePrefetchRecordsAndWarms(t *testing.T) {
	r := newPrefetchMetalRig(t, "", "", 256, "", false)
	r.wake(t)
	set := r.waitRecorded(t)
	if set.bytes <= 0 || set.bytes > restorePrefetchMaxBytes {
		t.Fatalf("recorded set of %d bytes, want (0, %d]", set.bytes, restorePrefetchMaxBytes)
	}
	if _, err := r.m.Park(context.Background(), "prefetch-metal", r.spec); err != nil {
		t.Fatalf("park: %v", err)
	}
	r.evict()
	if got := r.vmm.PrefetchRestore(r.snap.StorageKey); got != set.bytes {
		t.Fatalf("PrefetchRestore = %d bytes, want the recorded %d", got, set.bytes)
	}
	deadline := time.Now().Add(10 * time.Second)
	for residentBytes(t, r.memPath, set.ranges) < set.bytes {
		if time.Now().After(deadline) {
			t.Fatalf("prefetch left %d of %d recorded bytes uncached", set.bytes-residentBytes(t, r.memPath, set.ranges), set.bytes)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The prefetched snapshot still restores normally.
	r.wake(t)
}

// TestMetalRestorePrefetchBench is the ADR-225 evidence run: interleaved
// restores of one parked app with its mem evicted from the page cache, the
// working-set prefetch alternately disabled and enabled. Opt-in:
//
//	FAAS_RESTORE_PREFETCH_BENCH_CYCLES=24 (pairs of cycles)
//	FAAS_RESTORE_PREFETCH_BENCH_BASE / _LAYER  real production images
//	                                            (default: the v6 fixture)
//	FAAS_RESTORE_PREFETCH_BENCH_MEM_MIB=1024
//	FAAS_RESTORE_PREFETCH_BENCH_PREPARED=1     take each wake's network from
//	                                            the prepared pool (production
//	                                            shape: no inline netns setup
//	                                            for the prefetch to overlap)
func TestMetalRestorePrefetchBench(t *testing.T) {
	cycles, _ := strconv.Atoi(os.Getenv("FAAS_RESTORE_PREFETCH_BENCH_CYCLES"))
	if cycles <= 0 {
		t.Skip("set FAAS_RESTORE_PREFETCH_BENCH_CYCLES to run the prefetch A/B")
	}
	memMiB, _ := strconv.Atoi(os.Getenv("FAAS_RESTORE_PREFETCH_BENCH_MEM_MIB"))
	if memMiB <= 0 {
		memMiB = 1024
	}
	base, layer, health := os.Getenv("FAAS_RESTORE_PREFETCH_BENCH_BASE"), os.Getenv("FAAS_RESTORE_PREFETCH_BENCH_LAYER"), ""
	if base != "" {
		health = "/healthz"
	}
	r := newPrefetchMetalRig(t, base, layer, memMiB, health, os.Getenv("FAAS_RESTORE_PREFETCH_BENCH_PREPARED") == "1")
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	store := r.vmm.restorePrefetch
	// Record once from an evicted restore with prefetch enabled.
	r.evict()
	r.wake(t)
	set := r.waitRecorded(t)
	t.Logf("recorded working set: %d ranges, %.1f MiB", len(set.ranges), float64(set.bytes)/(1<<20))
	if _, err := r.m.Park(context.Background(), "prefetch-metal", r.spec); err != nil {
		t.Fatal(err)
	}
	type sample struct{ wall, total, resume int64 }
	arms := map[bool][]sample{}
	for i := 0; i < 2*cycles; i++ {
		on := i%2 == 1
		if on {
			r.vmm.restorePrefetch = store
		} else {
			r.vmm.restorePrefetch = nil
		}
		r.evict()
		capture.reset()
		wall := r.wake(t).Milliseconds()
		rows := capture.rows()
		if len(rows) != 1 {
			t.Fatalf("captured %d breakdowns, want 1", len(rows))
		}
		arms[on] = append(arms[on], sample{wall, rows[0]["total_ms"], rows[0]["resume_hook_ms"]})
		if on {
			r.waitRecorded(t)
		}
		if _, err := r.m.Park(context.Background(), "prefetch-metal", r.spec); err != nil {
			t.Fatal(err)
		}
	}
	r.vmm.restorePrefetch = store
	for _, on := range []bool{false, true} {
		pick := func(f func(sample) int64) []int64 {
			var v []int64
			for _, s := range arms[on] {
				v = append(v, f(s))
			}
			sort.Slice(v, func(a, b int) bool { return v[a] < v[b] })
			return v
		}
		for _, m := range []struct {
			name string
			f    func(sample) int64
		}{{"wall_ms", func(s sample) int64 { return s.wall }}, {"restore_total_ms", func(s sample) int64 { return s.total }}, {"resume_hook_ms", func(s sample) int64 { return s.resume }}} {
			v := pick(m.f)
			t.Logf("prefetch=%-5v %-17s n=%d min=%d p50=%d p90=%d max=%d", on, m.name, len(v), v[0], v[len(v)/2], v[(len(v)*9)/10], v[len(v)-1])
		}
	}
}
