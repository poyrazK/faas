//go:build metal

// adr: 192
// spec: §6.3

package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalRestoreTimingBench measures the per-phase cost of a real snapshot
// restore on a native KVM host. It is the reusable harness behind the §6.3
// wake budget: the acceptance gate (scripts/ops/wake_performance_gate.py)
// needs a whole control plane, which is exactly what a bare acceptance node
// does not have, so before this there was no way to attribute vmmd's own
// restore window on hardware.
//
// The measurement is the JailerVMM.Restore breakdown, captured from the
// "restore timing breakdown" Debug record rather than by changing the
// production signature — Restore deliberately returns only an error, and a
// benchmark is not a reason to widen it.
//
// Opt-in: set FAAS_RESTORE_BENCH_CYCLES=N. Unset, the test skips, so the
// metal gate's cost is unchanged. Each cycle is a full park→restore, so the
// snapshot is re-read from disk every time; the page cache stays warm across
// cycles, which is the steady-state a production wake sees on a node that is
// already serving.
//
//	FAAS_RESTORE_BENCH_CYCLES=30 make test-metal PKGS=./pkg/fcvm \
//	  RUN_REGEX=TestMetalRestoreTimingBench RUN_ARGS=-v
//
// FAAS_RESTORE_BENCH_JSON=<path> additionally writes one JSON object per
// cycle so two commits can be diffed offline.
func TestMetalRestoreTimingBench(t *testing.T) {
	cycles := benchCycles(t)

	// Self-contained fixture: the harness must run from a plain
	// `make test-metal` on any designated acceptance host, not only from
	// inside run-native-metal-smoke.sh (which builds its own guest and
	// exports FAAS_TEST_BASE_ROOTFS / FAAS_TEST_LAYER_ROOTFS). Only the
	// kernel is taken from the host, defaulting to the same path the smoke
	// script defaults to.
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	// Fixture placement is load-bearing for the measurement, not a detail.
	// reflinkCloneTemp creates drive1's clone NEXT TO ITS SOURCE, so a layer
	// on the test's default tmpdir cannot FICLONE and silently degrades to a
	// full copy — a first run measured stage_writable at 189 ms for a 64 MB
	// layer purely from that. Production layers live on the XFS reflink
	// volume, so the fixture must too or the number is fiction.
	dir := benchFixtureDir(t)
	base := filepath.Join(dir, "bench-base.ext4")
	layer := filepath.Join(dir, "bench-layer.ext4")
	if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
		t.Fatalf("build base fixture: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
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
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cycles+4)*time.Minute)
	defer cancel()

	const instance = "restore-bench"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/restore-bench/mem",
		VMStateStorageKey: "snap/restore-bench/vmstate",
		VMStatePath:       filepath.Join(snapDir, "vmstate"),
	}
	spec := SnapshotSpec{
		VMStatePath:       snap.VMStatePath,
		StorageKey:        snap.StorageKey,
		VMStateStorageKey: snap.VMStateStorageKey,
	}

	// Prime once: a cold boot produces the first snapshot. Everything measured
	// below is a restore of that same machine state, which is what a customer
	// wake does after the idle reaper has parked the app.
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer,
		VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	if _, err := m.Park(ctx, instance, spec); err != nil {
		t.Fatalf("prime park: %v", err)
	}

	// FAAS_RESTORE_BENCH_ENV_KEYS adds plaintext app env, which is the second
	// pre-boot writer alongside the service-discovery resolver. That is the
	// whole point of the ADR-192 A/B: before ADR-192 each writer took its own
	// loop mount of drive1, after it they share one. With the default of 0 the
	// harness measures the single-writer (resolver only) shape.
	apiEnv := benchAPIEnvEntries(t)
	t.Logf("pre-boot writers this run: resolver + %d api env keys", len(apiEnv))

	capture.reset()
	wall := make([]int64, 0, cycles)
	for i := 0; i < cycles; i++ {
		started := time.Now()
		out, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer,
			VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap,
			APIEnvEntries: apiEnv,
		})
		if err != nil {
			t.Fatalf("cycle %d: wake: %v", i, err)
		}
		if out.Method != WakeRestore {
			t.Fatalf("cycle %d: method = %s, want restore (the benchmark must not silently measure cold boots)", i, out.Method)
		}
		wall = append(wall, time.Since(started).Milliseconds())
		if _, err := m.Park(ctx, instance, spec); err != nil {
			t.Fatalf("cycle %d: park: %v", i, err)
		}
	}

	rows := capture.rows()
	if len(rows) != cycles {
		t.Fatalf("captured %d restore breakdowns, want %d — the Debug record or its message changed", len(rows), cycles)
	}
	for i, ms := range wall {
		rows[i]["wall_ms"] = ms
	}

	reportRestoreTiming(t, rows)
	if path := os.Getenv("FAAS_RESTORE_BENCH_JSON"); path != "" {
		writeRestoreTimingJSON(t, path, rows)
	}
}

func benchCycles(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("FAAS_RESTORE_BENCH_CYCLES")
	if raw == "" {
		t.Skip("set FAAS_RESTORE_BENCH_CYCLES=N to run the restore timing harness")
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 500 {
		t.Fatalf("FAAS_RESTORE_BENCH_CYCLES=%q must be an integer in [1,500]", raw)
	}
	return n
}

// restoreBreakdownCapture swaps the default slog logger for one that records
// every "restore timing breakdown" record at Debug. Restore emits the phases
// there for operators; reusing that record keeps the benchmark honest — it
// measures exactly what production logs, with no separate instrumentation to
// drift out of sync.
type restoreBreakdownCapture struct {
	mu       sync.Mutex
	captured []map[string]int64
	prev     *slog.Logger
}

func newRestoreBreakdownCapture(t *testing.T) *restoreBreakdownCapture {
	t.Helper()
	c := &restoreBreakdownCapture{prev: slog.Default()}
	slog.SetDefault(slog.New(&restoreBreakdownHandler{sink: c}))
	return c
}

func (c *restoreBreakdownCapture) restore() { slog.SetDefault(c.prev) }

func (c *restoreBreakdownCapture) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = nil
}

func (c *restoreBreakdownCapture) rows() []map[string]int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]int64(nil), c.captured...)
}

func (c *restoreBreakdownCapture) add(row map[string]int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.captured = append(c.captured, row)
}

type restoreBreakdownHandler struct {
	sink  *restoreBreakdownCapture
	attrs []slog.Attr
}

// Enabled reports Debug so the breakdown record is produced at all; the
// default handler level would drop it.
func (h *restoreBreakdownHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *restoreBreakdownHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &restoreBreakdownHandler{sink: h.sink, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *restoreBreakdownHandler) WithGroup(string) slog.Handler { return h }

func (h *restoreBreakdownHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Message != "restore timing breakdown" {
		return nil
	}
	row := map[string]int64{}
	r.Attrs(func(a slog.Attr) bool {
		if v, ok := attrMillis(a); ok {
			row[a.Key] = v
		}
		return true
	})
	if len(row) > 0 {
		h.sink.add(row)
	}
	return nil
}

func attrMillis(a slog.Attr) (int64, bool) {
	switch a.Value.Kind() {
	case slog.KindInt64:
		return a.Value.Int64(), true
	case slog.KindUint64:
		return int64(a.Value.Uint64()), true
	default:
		return 0, false
	}
}

// restoreTimingPhases is the report order: the wall clock first, then the
// phases that compose it, largest contributors of the 2026-09 production
// sample first so a regression is visible at the top of the table.
var restoreTimingPhases = []string{
	"wall_ms",
	"total_ms",
	"stage_pre_boot_files_ms",
	"resume_hook_ms",
	"stage_snapshot_ms",
	"load_snapshot_ms",
	"wait_ready_ms",
	"bind_tun_ms",
	"start_jailer_ms",
	"chroot_ms",
	"helper_ms",
	"materialize_mem_ms",
	"materialize_vmstate_ms",
	"mem_state_resolve_ms",
	"resolve_images_ms",
	"stage_drives_ms",
	"stage_writable_ms",
	"restore_gate_wait_ms",
}

func reportRestoreTiming(t *testing.T, rows []map[string]int64) {
	t.Helper()
	t.Logf("restore timing over %d park→restore cycles (nearest-rank percentiles, ms)", len(rows))
	t.Logf("%-24s %6s %6s %6s %6s %6s %6s", "phase", "min", "p50", "p90", "p95", "max", "mean")
	for _, phase := range restoreTimingPhases {
		vals := make([]int64, 0, len(rows))
		for _, r := range rows {
			if v, ok := r[phase]; ok {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			continue
		}
		sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
		var sum int64
		for _, v := range vals {
			sum += v
		}
		t.Logf("%-24s %6d %6d %6d %6d %6d %6.1f",
			phase, vals[0], nearestRank(vals, 50), nearestRank(vals, 90),
			nearestRank(vals, 95), vals[len(vals)-1], float64(sum)/float64(len(vals)))
	}
}

// nearestRank matches the percentile convention the rc.98 acceptance evidence
// used, so a run here is directly comparable to docs/ops/evidence.
func nearestRank(sorted []int64, pct int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (pct*len(sorted) + 99) / 100
	if idx < 1 {
		idx = 1
	}
	if idx > len(sorted) {
		idx = len(sorted)
	}
	return sorted[idx-1]
}

func writeRestoreTimingJSON(t *testing.T, path string, rows []map[string]int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for i, r := range rows {
		out := map[string]any{"cycle": i}
		for k, v := range r {
			out[k] = v
		}
		if err := enc.Encode(out); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	t.Logf("per-cycle rows written to %s", path)
}

// benchFixtureDir returns a directory on the production Firecracker volume so
// the drive1 reflink clone behaves as it does in production. Falls back to the
// ordinary tmpdir with a loud log line rather than skipping — a number with a
// stated caveat beats no number — but the fallback is NOT comparable to the
// production restore path.
func benchFixtureDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("FAAS_RESTORE_BENCH_FIXTURE_DIR")
	if root == "" {
		root = filepath.Join(filepath.Dir(JailChrootBase), "acceptance")
	}
	dir := filepath.Join(root, fmt.Sprintf("restore-bench-%d", os.Getpid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Logf("WARNING: fixture dir %s unavailable (%v); falling back to tmpdir — "+
			"stage_writable_ms will measure a full copy, not a reflink clone", dir, err)
		return t.TempDir()
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// stageBenchMountHelper reproduces production's mount-helper placement.
// newMetalVMM points mountHelperPath at the vmmd that `make test-metal`
// builds under the system tmpdir; stageMountHelper then hardlinks it into the
// tmpfs jail, gets EXDEV across filesystems, and copies the whole ~79 MB
// binary on EVERY restore — 231 ms of the first run's 519 ms total. Production
// never pays that: ensureMountHelper caches the binary under chrootBase, so
// the per-restore link is same-filesystem. Copy it there once, up front.
func stageBenchMountHelper(t *testing.T, v *JailerVMM) {
	t.Helper()
	src := v.mountHelperPath
	if src == "" {
		return
	}
	if err := os.MkdirAll(v.chrootBase, 0o700); err != nil {
		t.Fatalf("create chroot base %s: %v", v.chrootBase, err)
	}
	dst := filepath.Join(v.chrootBase, ".faas-bench-mount-helper")
	if err := copyFile(src, dst); err != nil {
		t.Fatalf("stage bench mount helper: %v", err)
	}
	if err := os.Chmod(dst, 0o755); err != nil {
		t.Fatalf("chmod bench mount helper: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(dst) })
	v.mountHelperPath = dst
}

// benchAPIEnvEntries synthesises N plaintext env rows. Values are padded to a
// realistic size so the write itself is not free, but the cost under test is
// the loop mount, not the bytes.
func benchAPIEnvEntries(t *testing.T) []APIEnvEntry {
	t.Helper()
	raw := os.Getenv("FAAS_RESTORE_BENCH_ENV_KEYS")
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 || n > 256 {
		t.Fatalf("FAAS_RESTORE_BENCH_ENV_KEYS=%q must be an integer in [0,256]", raw)
	}
	out := make([]APIEnvEntry, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, APIEnvEntry{
			Key:   fmt.Sprintf("BENCH_KEY_%03d", i),
			Value: strings.Repeat("v", 64),
		})
	}
	return out
}
