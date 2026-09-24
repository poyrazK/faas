//go:build metal

// spec: §14

// Metal-only tests for the vmmd-side cpustats.Cache
// (issue #279 / PR-B). The cache wraps cgroupstats.Reader with
// a per-instance previous-sample baseline and converts the raw
// cumulative usage_usec counter into a rate. The first test samples
// actual CPU usage from a disposable cgroup v2 leaf; the regression
// and Lookup tests feed deterministic observations to the cache.
//
// Acceptance gates:
//   - the cache observes a rate > 0 across a 300 ms window
//     when the underlying usage_usec delta is non-zero
//   - Lookup (the vmmd Stats hot path) returns the same Reading
//     the most recent Observe produced, without re-reading the
//     cgroup
//   - Regression (cgroup recreation) drops the baseline and the
//     next Observe returns Valid=false
//
// These are the §14 M8 acceptance gates for the vmmd-side CPU
// observability path. The cpustats package is the single source
// of truth for cpu_pct / cpu_seconds on the wire; PR-B's wire
// path is tested by `pkg/vmmdgrpc/stats_metal_test.go` (see
// also pkg/sched/instancestats/poller_metal_test.go for the
// schedd-side mirror of the same surface).

package cpustats

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// realCPUScope creates a disposable cgroup leaf and runs a busy shell inside
// it. cpu.stat is a kernel counter, not a regular file: tests must produce
// CPU work instead of attempting to overwrite its contents.
func realCPUScope(t *testing.T) string {
	t.Helper()
	root := "/sys/fs/cgroup"
	st, err := os.Stat(filepath.Join(root, "cgroup.controllers"))
	if err != nil || st.IsDir() {
		t.Skipf("cgroup v2 not detected at %s: %v", root, err)
	}
	name := "_test_cpustats_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	scope := filepath.Join(root, "faas-tenant.slice", name)
	if err := os.Mkdir(scope, 0o755); err != nil {
		t.Skipf("cannot mkdir under %s (read-only mount?): %v", root, err)
	}
	cmd := exec.Command("/bin/sh", "-c", "while :; do :; done")
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		var removeErr error
		for attempt := 0; attempt < 20; attempt++ {
			removeErr = os.Remove(scope)
			if removeErr == nil {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("remove test cgroup %s: %v", scope, removeErr)
	})
	if err := cmd.Start(); err != nil {
		t.Fatalf("start cgroup CPU workload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scope, "cgroup.procs"), []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		t.Fatalf("move CPU workload into test cgroup: %v", err)
	}
	return scope
}

func readCPUUsageUsec(t *testing.T, scope string) uint64 {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(scope, "cpu.stat"))
	if err != nil {
		t.Fatalf("read cpu.stat: %v", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "usage_usec" {
			continue
		}
		usage, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			t.Fatalf("parse cpu.stat usage_usec: %v", err)
		}
		return usage
	}
	t.Fatal("cpu.stat has no usage_usec counter")
	return 0
}

// TestMetalCache_ObserveComputesRateFromRealCgroup pins the
// cpustats.Cache's delta-math against a real cgroup v2 mount.
// A busy process advances the kernel counter during the sample window.
// The deterministic 40% calculation is covered in cache_test.go.
//
// This is the §14 M8 acceptance gate for the PR-B wire — the
// vmmd Stats handler reads CPUPct / CPUSeconds from this exact
// cache. A regression here would surface as "vmmd always reports
// 0 % CPU" on the box and "schedd_instance_cpu_pct gauge never
// increments" in Prometheus.
func TestMetalCache_ObserveComputesRateFromRealCgroup(t *testing.T) {
	scope := realCPUScope(t)
	instance := filepath.Base(scope)
	cache := New(nil)
	t0 := time.Now()
	initUsec := readCPUUsageUsec(t, scope)

	_, ok := cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: initUsec, At: t0})
	if ok {
		t.Fatal("first Observe returned ok=true — the baseline branch should always return ok=false")
	}

	time.Sleep(300 * time.Millisecond)
	t1 := time.Now()
	nextUsec := readCPUUsageUsec(t, scope)
	r, ok := cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: nextUsec, At: t1})
	if !ok {
		t.Fatal("second Observe returned ok=false — the cache should produce a valid reading after the baseline")
	}
	if !r.Valid {
		t.Fatal("Reading.Valid = false on the second observation — expected true")
	}
	if nextUsec <= initUsec {
		t.Fatalf("kernel usage_usec did not advance: before=%d after=%d", initUsec, nextUsec)
	}
	if r.CPUPct <= 0 || r.CPUPct > 400 {
		t.Errorf("CPUPct = %v, want a positive bounded rate from real cpu.stat", r.CPUPct)
	}
	wantSeconds := float64(nextUsec-initUsec) / 1e6
	if r.CPUSeconds != wantSeconds {
		t.Errorf("CPUSeconds = %v, want %v from real cpu.stat delta", r.CPUSeconds, wantSeconds)
	}
}

// TestMetalCache_RegressionDropsBaseline verifies the cache's
// "cgroup recreation" contract. On a usage_usec step-down
// (smaller-than-prev reading), the cache MUST drop the baseline
// and return Valid=false from the next Observe. This is the
// behaviour the schedd-side poller relies on to stamp
// CPU=Unknown on the first post-regression row
// (pkg/sched/instancestats/reader.go).
func TestMetalCache_RegressionDropsBaseline(t *testing.T) {
	instance := "_test_cpustats_regression_" + strconv.FormatInt(time.Now().UnixNano(), 36)

	cache := New(func() time.Time { return time.Unix(0, 0) })
	t0 := time.Unix(0, 0)

	// Establish a baseline at t0.
	cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: 1_000_000, At: t0})

	// Simulate a jailer restart: a fresh cgroup starts its
	// usage_usec counter at 0 and ticks up. The cache's
	// regression branch MUST drop the baseline.
	r, ok := cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: 50_000, At: t0.Add(100 * time.Millisecond)})
	if ok {
		t.Errorf("post-regression Observe returned ok=true (r=%+v) — the cache should drop the baseline on a step-down", r)
	}
	if r.Valid {
		t.Errorf("Reading.Valid = true on post-regression row — expected false (Unknown on the schedd side)")
	}

	// The next post-regression observation should return a
	// valid reading — the cache has re-baselined.
	r, ok = cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: 75_000, At: t0.Add(250 * time.Millisecond)})
	if !ok || !r.Valid {
		t.Fatalf("post-regression re-baseline Observe returned ok=%v valid=%v — expected a valid reading on the next sample", ok, r.Valid)
	}
	// 25_000 µs over 150 ms = 100 * (25_000 / 1e6) / 0.150 ≈ 16.67 %
	const wantPct = 16.667
	const tolerance = 0.05
	if r.CPUPct < wantPct-tolerance || r.CPUPct > wantPct+tolerance {
		t.Errorf("CPUPct = %v, want ~%v (post-regression re-baseline rate)", r.CPUPct, wantPct)
	}
}

// TestMetalCache_LookupDoesNotAdvanceBaseline pins the vmmd
// Stats hot-path contract: Lookup returns the cached rate
// without advancing the baseline or re-reading the cgroup. The
// schedd 200 ms poller calls vmmd Stats, which calls Lookup,
// once per tick per instance. If Lookup mutated the baseline,
// the next Observe would compute a zero delta and the rate
// would appear to alternate between real and zero in /metrics.
func TestMetalCache_LookupDoesNotAdvanceBaseline(t *testing.T) {
	instance := "_test_cpustats_lookup_" + strconv.FormatInt(time.Now().UnixNano(), 36)

	cache := New(func() time.Time { return time.Unix(0, 0) })
	t0 := time.Unix(0, 0)

	// Baseline
	cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: 1_000_000, At: t0})
	// First non-baseline observation
	first, ok := cache.Observe(Observation{InstanceID: instance, CPUUsageUsec: 1_100_000, At: t0.Add(250 * time.Millisecond)})
	if !ok {
		t.Fatal("second Observe returned ok=false")
	}

	// Many Lookup calls — the rate must stay stable.
	for i := 0; i < 10; i++ {
		got, ok := cache.Lookup(instance)
		if !ok {
			t.Fatalf("Lookup[%d] returned ok=false — the cache should retain the rate", i)
		}
		if got.CPUPct != first.CPUPct {
			t.Errorf("Lookup[%d].CPUPct = %v, want %v (Lookup must not advance the baseline)", i, got.CPUPct, first.CPUPct)
		}
		if got.CPUSeconds != first.CPUSeconds {
			t.Errorf("Lookup[%d].CPUSeconds = %v, want %v (Lookup must not advance the accumulator)", i, got.CPUSeconds, first.CPUSeconds)
		}
	}
}
