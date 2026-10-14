//go:build metal

// adr: 977
// spec: §6.3

package fcvm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalConcurrentRestoreBench measures what a snapshot restore costs while
// other restores run at the same time, phase by phase. Service wake-ahead
// (ADR-977) restores a caller and its dependencies together; on the n2 node a
// pair of restores each took 1.2–2.1 s against 0.7–1.45 s for the same
// restores one after the other, which ate most of the overlap. This harness
// finds the phase that grows.
//
// Every cycle restores one VM alone, then K VMs at once, so host drift hits
// both shapes alike. Breakdowns come from the same "restore timing breakdown"
// Debug record as TestMetalRestoreTimingBench.
//
// Opt-in: FAAS_RESTORE_BENCH_CONCURRENT_CYCLES=N (1–200).
// FAAS_RESTORE_BENCH_CONCURRENCY=K (2–8, default 2) sets the group size.
func TestMetalConcurrentRestoreBench(t *testing.T) {
	cycles := benchEnvInt(t, "FAAS_RESTORE_BENCH_CONCURRENT_CYCLES", 0, 1, 200)
	if cycles == 0 {
		t.Skip("set FAAS_RESTORE_BENCH_CONCURRENT_CYCLES=N to run the concurrent restore harness")
	}
	group := benchEnvInt(t, "FAAS_RESTORE_BENCH_CONCURRENCY", 2, 2, 8)
	memMiB := benchMemMiB(t)

	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	dir := benchFixtureDir(t)
	base := filepath.Join(dir, "bench-base.ext4")
	layer := filepath.Join(dir, "bench-layer.ext4")
	if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
		t.Fatalf("build base fixture: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, benchLayerMiB(t)); err != nil {
		t.Fatalf("build layer fixture: %v", err)
	}
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

	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(store)
	stageBenchMountHelper(t, vmm)
	warnings := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, warnings, nil)
	if n := benchPreparedNetworks(t); n > 0 {
		if err := m.EnablePreparedNetworks(context.Background(), n); err != nil {
			t.Fatalf("enable prepared networks: %v", err)
		}
		t.Cleanup(func() { _ = m.ClosePreparedNetworks() })
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(2*cycles+2*group+4)*time.Minute)
	defer cancel()

	type benchVM struct {
		instance string
		snap     *Snapshot
		spec     SnapshotSpec
	}
	vms := make([]benchVM, group)
	for i := range vms {
		name := fmt.Sprintf("restore-bench-c%d", i)
		snap := &Snapshot{
			FCVersion:         fcVersion,
			StorageKey:        "snap/" + name + "/mem",
			VMStateStorageKey: "snap/" + name + "/vmstate",
			VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
		}
		vms[i] = benchVM{instance: name, snap: snap, spec: SnapshotSpec{
			VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
		}}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), name) })
		if _, err := m.ColdBoot(ctx, ColdBootRequest{
			Instance: name, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB,
		}); err != nil {
			t.Fatalf("prime cold boot %s: %v", name, err)
		}
		if _, err := m.Park(ctx, name, vms[i].spec); err != nil {
			t.Fatalf("prime park %s: %v", name, err)
		}
	}
	wake := func(v benchVM) (int64, error) {
		started := time.Now()
		out, err := m.Wake(ctx, WakeRequest{
			Instance: v.instance, Plan: "pro", BaseKey: base, LayerKey: layer,
			VcpuCount: 2, MemSizeMiB: memMiB, Snapshot: v.snap,
		})
		if err != nil {
			return 0, err
		}
		if out.Method != WakeRestore {
			return 0, fmt.Errorf("method = %s, want restore", out.Method)
		}
		return time.Since(started).Milliseconds(), nil
	}
	park := func(vs []benchVM) {
		t.Helper()
		for _, v := range vs {
			if _, err := m.Park(ctx, v.instance, v.spec); err != nil {
				t.Fatalf("park %s: %v", v.instance, err)
			}
		}
	}

	t.Logf("host: %d CPUs; guest: %d MiB; group of %d", runtime.NumCPU(), memMiB, group)
	var alone, together []map[string]int64
	for i := 0; i < cycles; i++ {
		capture.reset()
		ms, err := wake(vms[0])
		if err != nil {
			t.Fatalf("cycle %d alone: %v", i, err)
		}
		rows := capture.rows()
		if len(rows) != 1 {
			t.Fatalf("cycle %d alone: captured %d breakdowns, want 1", i, len(rows))
		}
		rows[0]["wall_ms"] = ms
		alone = append(alone, rows...)
		park(vms[:1])

		capture.reset()
		walls := make([]int64, group)
		errs := make([]error, group)
		var wg sync.WaitGroup
		for j := range vms {
			wg.Add(1)
			go func() {
				defer wg.Done()
				walls[j], errs[j] = wake(vms[j])
			}()
		}
		wg.Wait()
		for j, err := range errs {
			if err != nil {
				t.Fatalf("cycle %d together, %s: %v", i, vms[j].instance, err)
			}
		}
		rows = capture.rows()
		if len(rows) != group {
			t.Fatalf("cycle %d together: captured %d breakdowns, want %d", i, len(rows), group)
		}
		// Breakdowns arrive in completion order and every wake started at
		// once, so the shortest wall belongs to the first breakdown.
		sort.Slice(walls, func(a, b int) bool { return walls[a] < walls[b] })
		for j := range rows {
			rows[j]["wall_ms"] = walls[j]
		}
		together = append(together, rows...)
		park(vms)
	}

	t.Logf("=== one restore alone ===")
	reportRestoreTiming(t, alone)
	t.Logf("=== %d restores at once ===", group)
	reportRestoreTiming(t, together)
	reportRestoreGrowth(t, alone, together)
}

// reportRestoreGrowth lines up each phase's median alone and under
// concurrency, so the phase that absorbs the contention is the one that grows.
func reportRestoreGrowth(t *testing.T, alone, together []map[string]int64) {
	t.Helper()
	median := func(rows []map[string]int64, phase string) (int64, bool) {
		vals := make([]int64, 0, len(rows))
		for _, r := range rows {
			if v, ok := r[phase]; ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return 0, false
		}
		sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
		return nearestRank(vals, 50), true
	}
	t.Logf("%-26s %8s %8s %8s", "phase p50", "alone", "together", "growth")
	for _, phase := range restoreTimingPhases {
		a, okA := median(alone, phase)
		b, okB := median(together, phase)
		if !okA && !okB {
			continue
		}
		t.Logf("%-26s %8d %8d %+8d", phase, a, b, b-a)
	}
}

// benchEnvInt reads an integer knob, def when unset, failing outside [lo, hi].
func benchEnvInt(t *testing.T, name string, def, lo, hi int) int {
	t.Helper()
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		t.Fatalf("%s=%q must be an integer in [%d,%d]", name, raw, lo, hi)
	}
	return n
}
