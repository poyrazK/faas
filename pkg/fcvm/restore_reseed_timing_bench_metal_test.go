//go:build metal

// adr: 680 — the userspace RNG reseed barrier gates every Node/Python restore;
// this benchmark prices it against the reseed-off control on real runtimes.

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
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalRestoreReseedTimingBench restores one snapshot of a real Node and
// Python process N times with the ADR-680 reseed on and off, and reports the
// restore phases plus first-request latency for each. Production-us measured
// resume_hook p50 Go 57 / Python 141 / Node 421 ms (hunt #9, 2026-10-11): the
// difference between the reseed-on and reseed-off rows is the barrier's cost
// on this host, separated from the kernel resume work every runtime pays.
//
// Each runtime runs three arms: reseed-off (no barrier), reseed-on (the
// event-loop client) and reseed-thread (ADR-20261024105607: Node answers from
// the addon's native thread). FAAS_RESEED_BENCH_ARMS and
// FAAS_RESEED_BENCH_RUNTIMES (comma lists) select a subset.
// FAAS_RESEED_BENCH_REQUEST_DELAY_MS waits that long between the restore and
// the first request, standing in for the platform's work after readiness
// (schedd publication, gateway routing); first_byte excludes the wait.
// FAAS_RESEED_BENCH_VCPUS sets the guest's vCPUs (default 1).
//
//	FAAS_TEST_RNG_NODE_ROOTFS=/var/tmp/rng-metal/node22.tar \
//	FAAS_TEST_RNG_PYTHON_ROOTFS=/var/tmp/rng-metal/python313.tar \
//	FAAS_RESEED_BENCH_CYCLES=10 \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalRestoreReseedTimingBench RUN_ARGS=-v
func TestMetalRestoreReseedTimingBench(t *testing.T) {
	cycles := 8
	if raw := os.Getenv("FAAS_RESEED_BENCH_CYCLES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			t.Fatalf("FAAS_RESEED_BENCH_CYCLES=%q must be an integer in [1,200]", raw)
		}
		cycles = n
	} else if os.Getenv("FAAS_TEST_RNG_NODE_ROOTFS") == "" && os.Getenv("FAAS_TEST_RNG_PYTHON_ROOTFS") == "" {
		t.Skip("set FAAS_TEST_RNG_NODE_ROOTFS and/or FAAS_TEST_RNG_PYTHON_ROOTFS (and optionally FAAS_RESEED_BENCH_CYCLES)")
	}
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		kernel = "/srv/fc/base/vmlinux-6.1.134"
	}
	if _, err := os.Stat(kernel); err != nil {
		t.Skipf("kernel %s unavailable: %v (set FAAS_TEST_KERNEL)", kernel, err)
	}
	runtimes := []struct {
		name, rootfsEnv, script, file string
		entrypoint                    []string
	}{
		{name: "node", rootfsEnv: "FAAS_TEST_RNG_NODE_ROOTFS", file: "srv/rng-app.js", script: rngNodeApp,
			entrypoint: []string{"/usr/local/bin/node", "/srv/rng-app.js"}},
		// Production Node apps start through `npm run start`: npm itself is a
		// second Node process carrying the preload, so the barrier waits on
		// it too.
		{name: "node-npm", rootfsEnv: "FAAS_TEST_RNG_NODE_ROOTFS", file: "srv/rng-app.js", script: rngNodeApp,
			entrypoint: []string{"/bin/sh", "-c", "PATH=/usr/local/bin:/usr/bin:/bin HOME=/tmp npm_config_cache=/tmp/.npm npm exec --offline --yes=false -- node /srv/rng-app.js"}},
		// An app that never calls randomUUID or randomInt: what the barrier
		// costs when nothing reads the RNG caches after a restore.
		{name: "node-plain", rootfsEnv: "FAAS_TEST_RNG_NODE_ROOTFS", file: "srv/rng-app.js", script: plainNodeApp,
			entrypoint: []string{"/usr/local/bin/node", "/srv/rng-app.js"}},
		{name: "python", rootfsEnv: "FAAS_TEST_RNG_PYTHON_ROOTFS", file: "srv/rng-app.py", script: rngPythonApp,
			entrypoint: []string{"/usr/bin/python3", "/srv/rng-app.py"}},
	}
	opts := reseedBenchOptions{
		vcpus:        reseedBenchInt(t, "FAAS_RESEED_BENCH_VCPUS", 1, 1, 8),
		requestDelay: time.Duration(reseedBenchInt(t, "FAAS_RESEED_BENCH_REQUEST_DELAY_MS", 0, 0, 5000)) * time.Millisecond,
	}
	arms := []struct {
		name   string
		env    map[string]string
		thread bool
	}{
		{name: "reseed-off", env: map[string]string{"GREGALE_RESTORE_RESEED": "off"}},
		{name: "reseed-on", env: map[string]string{}},
		{name: "reseed-thread", env: map[string]string{}, thread: true},
	}
	for _, rt := range runtimes {
		tarball := os.Getenv(rt.rootfsEnv)
		if tarball == "" || !reseedBenchSelected("FAAS_RESEED_BENCH_RUNTIMES", rt.name) {
			continue
		}
		for _, arm := range arms {
			if !reseedBenchSelected("FAAS_RESEED_BENCH_ARMS", arm.name) {
				continue
			}
			name := rt.name + "/" + arm.name
			t.Run(name, func(t *testing.T) {
				opts := opts
				opts.thread = arm.thread
				rows, requests := restoreSnapshotN(t, kernel, tarball, rt.file, rt.script, rt.entrypoint, arm.env, cycles, opts)
				reportRestoreTiming(t, rows)
				if dir := os.Getenv("FAAS_RESEED_BENCH_JSON_DIR"); dir != "" {
					for i := range rows {
						if i < len(requests) {
							rows[i]["bench_wake_us"] = requests[i].wake.Microseconds()
							rows[i]["bench_first_request_us"] = requests[i].request.Microseconds()
						}
					}
					writeRestoreTimingJSON(t, filepath.Join(dir, rt.name+"-"+arm.name+".json"), rows)
				}
				for _, metric := range []struct {
					label string
					pick  func(reseedBenchRequest) int64
				}{
					{"first request after wake", func(r reseedBenchRequest) int64 { return r.request.Milliseconds() }},
					{"first byte (wake + first request)", func(r reseedBenchRequest) int64 { return (r.wake + r.request).Milliseconds() }},
				} {
					vals := make([]int64, 0, len(requests))
					for _, r := range requests {
						vals = append(vals, metric.pick(r))
					}
					sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
					t.Logf("%s %s (ms, request delay %s): p50=%d p90=%d max=%d", name, metric.label, opts.requestDelay,
						nearestRank(vals, 50), nearestRank(vals, 90), vals[len(vals)-1])
				}
			})
		}
	}
}

type reseedBenchOptions struct {
	thread       bool
	vcpus        int
	requestDelay time.Duration
}

// reseedBenchRequest is one restore's Manager.Wake duration and its first
// request's latency, measured apart from any configured delay between them.
type reseedBenchRequest struct {
	wake, request time.Duration
}

func reseedBenchInt(t *testing.T, key string, def, lo, hi int) int {
	t.Helper()
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		t.Fatalf("%s=%q must be an integer in [%d,%d]", key, raw, lo, hi)
	}
	return n
}

func reseedBenchSelected(key, name string) bool {
	raw := os.Getenv(key)
	if raw == "" {
		return true
	}
	for _, item := range strings.Split(raw, ",") {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	return false
}

// restoreSnapshotN cold-boots the app, parks it into one snapshot, then
// restores that snapshot n times, destroying each instance before the next.
// It returns each restore's Debug breakdown and the first request latency.
func restoreSnapshotN(t *testing.T, kernel, tarball, file, script string, entrypoint []string, env map[string]string, n int, opts reseedBenchOptions) ([]map[string]int64, []reseedBenchRequest) {
	t.Helper()
	dir := benchFixtureDir(t)
	base := filepath.Join(dir, "reseed-base.ext4")
	layer := filepath.Join(dir, "reseed-layer.ext4")
	if err := buildRuntimeBaseExt4(base, repoRoot(t), tarball, file, script, entrypoint, env); err != nil {
		t.Fatalf("build runtime base: %v", err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatalf("build layer: %v", err)
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
	vmm := newMetalVMM(t, 60*time.Second).WithStorage(store).WithRestoreReseedThread(opts.thread)
	stageBenchMountHelper(t, vmm)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	const instance = "reseed-bench"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	memMiB := benchMemMiB(t)
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: opts.vcpus, MemSizeMiB: memMiB,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	// Let the runtime settle (JIT, timers) the way a parked production
	// instance has, then capture.
	time.Sleep(2 * time.Second)
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/reseed-bench/mem",
		VMStateStorageKey: "snap/reseed-bench/vmstate",
		VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
	}); err != nil {
		t.Fatalf("park: %v", err)
	}
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	client := &http.Client{Timeout: 30 * time.Second}
	requests := make([]reseedBenchRequest, 0, n)
	for round := 1; round <= n; round++ {
		wakeStarted := time.Now()
		out, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer,
			VcpuCount: opts.vcpus, MemSizeMiB: memMiB, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("restore %d: %v", round, err)
		}
		woke := time.Since(wakeStarted)
		if out.Method != WakeRestore {
			t.Fatalf("restore %d: method = %s, want restore", round, out.Method)
		}
		time.Sleep(opts.requestDelay)
		started := time.Now()
		resp, err := client.Get(fmt.Sprintf("http://%s:8080/", out.Lease.HostIP))
		if err != nil {
			t.Fatalf("restore %d: request: %v", round, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		requests = append(requests, reseedBenchRequest{wake: woke, request: time.Since(started)})
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("destroy after restore %d: %v", round, err)
		}
	}
	return capture.rows(), requests
}

const plainNodeApp = `require("http").createServer((req, res) => res.end("ok")).listen(8080, "0.0.0.0");
`
