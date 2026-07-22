//go:build metal

// M7 function-wake latency gate (spec §14):
//
//	function hello-world p95 wake < 1 s
//
// Mirrors TestMetalParkWakeCycle (the M3 acceptance gate) but uses a
// minimal function-shaped rootfs (busybox + a sentinel /init that signals
// readiness, the same harness M0 already validates) and asserts the
// stricter M7 threshold. The point is to measure the platform-side wake
// path under the same restore-from-snapshot loop the function runtime
// hits in production — the function runner binary itself is not on the
// critical path here (it loads lazily after Wake returns and the proxy
// forwards the request), so the busybox /init harness is a faithful
// stand-in for the wake portion of the gate.
//
// The test is intentionally cheap: 30 cycles, single VM, the same
// Manager.ColdBoot/Park/Wake loop schedd's engine drives on every
// cron-fired request. A wake p95 > 1 s surfaces a regression in the
// snapshot-restore path (most likely the cache fsyncing or the
// netns/tap setup) before it ships.
//
// Same env vars as TestMetalHelloBoot: FAAS_TEST_KERNEL.
package fcvm

import (
	"context"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalFunctionWakeP95 is the M7 function-wake acceptance gate
// (spec §14). 30 cycles of cold-boot → snapshot → restore. We log
// p50/p95/max so the dashboard can chart the trend across runs.
func TestMetalFunctionWakeP95(t *testing.T) {
	kernel, _, _ := metalImages(t)
	tmp := t.TempDir()
	rootfs := ensureBusyboxExt4(t, tmp)

	fcVer, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	// Visible logger so the fallback warn ("restore failed; cold-boot
	// fallback") is observable from the test output — otherwise a
	// silent PlanWake→WakeColdBoot looks like a snapshot regression.
	m := NewManager(
		wire.ExecRunner{},
		NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel},
		fcVer,
		slog.New(slog.NewTextHandler(testLogWriter{t}, nil)),
		nil,
	)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion:   fcVer,
		StorageKey:  "snap/" + instance + "/mem",
		VMStatePath: snapDir + "/vmstate",
	}

	// Prime: cold boot once with the function-shaped rootfs so the
	// first Park produces a snapshot schedd will actually reuse.
	const instance = "m7-func"
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, BaseKey: rootfs, LayerKey: rootfs,
		VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey,
	}); err != nil {
		t.Fatalf("prime park: %v", err)
	}

	const cycles = 30
	latencies := make([]time.Duration, 0, cycles)
	for i := 0; i < cycles; i++ {
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, BaseKey: rootfs, LayerKey: rootfs,
			VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("wake cycle %d: %v", i, err)
		}
		latencies = append(latencies, time.Since(start))
		if inst.Method != WakeRestore {
			// A restore that fell back to cold boot is a snapshot-path
			// regression; fail fast instead of misleading the latency
			// numbers.
			t.Fatalf("cycle %d fell back to %s — snapshot restore regressed", i, inst.Method)
		}
		if _, err := m.Park(ctx, instance, SnapshotSpec{
			VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey,
		}); err != nil {
			t.Fatalf("park cycle %d: %v", i, err)
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)/2]
	p95 := latencies[len(latencies)*95/100]
	max := latencies[len(latencies)-1]
	t.Logf("function wake latency over %d cycles: p50=%s p95=%s max=%s", cycles, p50, p95, max)

	// M7 gate: function hello-world p95 wake < 1 s (spec §14).
	// The local-loop Lima path runs arm64 nested-KVM which is slower
	// than the EX44's bare-metal x86_64 Firecracker. We still gate at
	// the spec threshold so a regression on the box-side hot path
	// catches here too — if it fails on Lima it will fail on the EX44.
	if p95 >= 1*time.Second {
		t.Errorf("function wake p95 = %s, want < 1 s (spec §14 M7 gate)", p95)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases after cycles: %d", m.LeasedCount())
	}
	if m.LiveCount() != 0 {
		t.Errorf("live instances after cycles: %d (want 0)", m.LiveCount())
	}
}

// testLogWriter routes slog records into the test's t.Log so the
// "restore failed; cold-boot fallback" warn surfaces when the
// snapshot path regresses.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}
