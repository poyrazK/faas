//go:build linux && metal

// adr: 149 — prepared-network reuse reduces restore work without retaining guests.
package fcvm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// This diagnostic measures Manager.Wake plus the first direct guest response,
// not gateway/scheduler latency. Run only in private mount/network namespaces
// with an isolated jail and a freshly built vmmd/helper. Both A/B binaries use
// this same harness; only prepared_network.go differs between them.
func TestMetalPreparedNetworkRestoreTiming(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires isolated mount/network namespaces and jail")
	}
	cycles := benchCycles(t)
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
	fixture := benchFixtureDir(t)
	base, layer := filepath.Join(fixture, "base.ext4"), filepath.Join(fixture, "layer.ext4")
	if err := buildV6BaseExt4(base, repoRoot(t)); err != nil {
		t.Fatal(err)
	}
	if err := buildV6LayerExt4Size(layer, 64); err != nil {
		t.Fatal(err)
	}
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	if kernel == "" {
		t.Fatal("set FAAS_TEST_KERNEL")
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatal(err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatal(err)
	}
	version, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	capture := newRestoreBreakdownCapture(t)
	defer capture.restore()
	vmm := newMetalVMM(t, 30*time.Second).WithStorage(store)
	stageBenchMountHelper(t, vmm)
	m := NewManager(run, vmm, Paths{Kernel: kernel}, version, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	if err := m.EnablePreparedNetworks(ctx, 3); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.ClosePreparedNetworks(); err != nil {
			t.Error(err)
		}
	})
	const instance = "prepared-network-bench"
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "scale", BaseKey: base, LayerKey: layer,
		VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 250, Port: netns.AppPort, EgressMbit: 250,
	}); err != nil {
		t.Fatal(err)
	}
	memKey := state.SnapshotCaptureMemKey(instance, state.SnapshotTierWarm, "initial")
	stateKey := state.SnapshotVMStateKey(state.Snapshot{StorageKey: memKey})
	snapshot := &Snapshot{FCVersion: version, StorageKey: memKey, VMStateStorageKey: stateKey,
		VMStatePath: filepath.Join(t.TempDir(), "vmstate")}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		StorageKey: memKey, VMStateStorageKey: stateKey, VMStatePath: snapshot.VMStatePath,
	}); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	seen := map[string]bool{}
	var measurements []map[string]int64
	capture.reset()
	for cycle := 0; cycle < cycles; cycle++ {
		waitPreparedBenchmarkNetwork(t, m, ctx)
		if m.LiveCount() != 0 || m.LeasedCount() != 0 {
			t.Fatal("benchmark must begin with no resident guest")
		}
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "scale", BaseKey: base, LayerKey: layer,
			VcpuCount: 2, MemSizeMiB: 128, CPUMillicores: 250, Port: netns.AppPort,
			EgressMbit: 250, Snapshot: snapshot,
		})
		if err != nil {
			t.Fatalf("cycle %d: %v", cycle, err)
		}
		wake := time.Since(start)
		if inst.Method != WakeRestore {
			t.Fatalf("cycle %d: cold fallback: %s", cycle, inst.RestoreError)
		}
		id, headers := preparedBenchmarkResponse(t, client, inst, start)
		if seen[id] {
			t.Fatal("restores reused a UUID")
		}
		seen[id] = true
		measurements = append(measurements, map[string]int64{
			"wall_ms": wake.Milliseconds(), "setup_network_ms": inst.NetnsTapMs,
			"wake_to_headers_ms": headers.Milliseconds(), "response_complete_ms": time.Since(start).Milliseconds(),
		})
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatal(err)
		}
	}
	rows := capture.rows()
	if len(rows) != cycles {
		t.Fatalf("restore breakdown count=%d, want %d", len(rows), cycles)
	}
	for i, measurement := range measurements {
		for key, value := range measurement {
			rows[i][key] = value
		}
	}
	reportRestoreTiming(t, rows)
	if path := os.Getenv("FAAS_RESTORE_BENCH_JSON"); path != "" {
		writeRestoreTimingJSON(t, path, rows)
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("benchmark left a live guest or admitted lease")
	}
}

func waitPreparedBenchmarkNetwork(t *testing.T, m *Manager, ctx context.Context) {
	t.Helper()
	policy, ok := m.preparedPolicy(WakeRequest{Plan: "scale", Port: netns.AppPort, EgressMbit: 250})
	if !ok {
		t.Fatal("benchmark request is ineligible for prepared networks")
	}
	m.preparedNetworks.observe(policy)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		m.preparedNetworks.mu.Lock()
		ready := len(m.preparedNetworks.ready)
		m.preparedNetworks.mu.Unlock()
		if ready > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatal("prepared-network pool did not refill")
		case <-time.After(time.Millisecond):
		}
	}
}

func preparedBenchmarkResponse(t *testing.T, client *http.Client, inst *Instance, start time.Time) (string, time.Duration) {
	t.Helper()
	resp, err := client.Get("http://" + inst.Lease.HostIP.String() + ":8080/etc/faas/uuid.txt")
	headers := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil || resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) == "" {
		t.Fatal(fmt.Errorf("first response: status=%d read_error=%v", resp.StatusCode, err))
	}
	return strings.TrimSpace(string(body)), headers
}
