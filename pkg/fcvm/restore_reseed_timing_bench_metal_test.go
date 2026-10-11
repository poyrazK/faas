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
		{name: "python", rootfsEnv: "FAAS_TEST_RNG_PYTHON_ROOTFS", file: "srv/rng-app.py", script: rngPythonApp,
			entrypoint: []string{"/usr/bin/python3", "/srv/rng-app.py"}},
	}
	for _, rt := range runtimes {
		tarball := os.Getenv(rt.rootfsEnv)
		if tarball == "" {
			continue
		}
		for _, reseed := range []bool{false, true} {
			name := rt.name + "/reseed-off"
			env := map[string]string{"GREGALE_RESTORE_RESEED": "off"}
			if reseed {
				name, env = rt.name+"/reseed-on", map[string]string{}
			}
			t.Run(name, func(t *testing.T) {
				rows, firstRequest := restoreSnapshotN(t, kernel, tarball, rt.file, rt.script, rt.entrypoint, env, cycles)
				reportRestoreTiming(t, rows)
				sort.Slice(firstRequest, func(i, j int) bool { return firstRequest[i] < firstRequest[j] })
				t.Logf("%s first request after wake (ms): p50=%d p90=%d max=%d", name,
					nearestRank(firstRequest, 50), nearestRank(firstRequest, 90), firstRequest[len(firstRequest)-1])
			})
		}
	}
}

// restoreSnapshotN cold-boots the app, parks it into one snapshot, then
// restores that snapshot n times, destroying each instance before the next.
// It returns each restore's Debug breakdown and the first request latency.
func restoreSnapshotN(t *testing.T, kernel, tarball, file, script string, entrypoint []string, env map[string]string, n int) ([]map[string]int64, []int64) {
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
	vmm := newMetalVMM(t, 60*time.Second).WithStorage(store)
	stageBenchMountHelper(t, vmm)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	const instance = "reseed-bench"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	memMiB := benchMemMiB(t)
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer, VcpuCount: 1, MemSizeMiB: memMiB,
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
	firstRequest := make([]int64, 0, n)
	for round := 1; round <= n; round++ {
		out, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layer,
			VcpuCount: 1, MemSizeMiB: memMiB, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("restore %d: %v", round, err)
		}
		if out.Method != WakeRestore {
			t.Fatalf("restore %d: method = %s, want restore", round, out.Method)
		}
		started := time.Now()
		resp, err := client.Get(fmt.Sprintf("http://%s:8080/", out.Lease.HostIP))
		if err != nil {
			t.Fatalf("restore %d: request: %v", round, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		firstRequest = append(firstRequest, time.Since(started).Milliseconds())
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("destroy after restore %d: %v", round, err)
		}
	}
	return capture.rows(), firstRequest
}
