//go:build metal

// Metal integration tests: need /dev/kvm, root, firecracker + jailer on PATH,
// and real kernel/base/layer images. Run on the dev EX44 via `make test-metal`.
// Executable acceptance criteria (spec §14): M1 — boot 50 × 128 MB VMs
// concurrently and leak zero netns/TAPs/uids on teardown; M3 — park→wake p50
// ≤ 350 ms over 100 cycles restoring from a snapshot each time.
package fcvm

import (
	"context"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// metalImages resolves the kernel/base/layer paths from the environment so the
// test is portable across boxes. Skips if unset.
func metalImages(t *testing.T) (kernel, base, layer string) {
	t.Helper()
	kernel = os.Getenv("FAAS_TEST_KERNEL")
	base = os.Getenv("FAAS_TEST_BASE_ROOTFS")
	layer = os.Getenv("FAAS_TEST_LAYER_ROOTFS")
	if kernel == "" || base == "" || layer == "" {
		t.Skip("set FAAS_TEST_KERNEL / FAAS_TEST_BASE_ROOTFS / FAAS_TEST_LAYER_ROOTFS to run metal tests")
	}
	return kernel, base, layer
}

func newMetalManager(t *testing.T, kernel string) *Manager {
	t.Helper()
	return NewManager(
		wire.ExecRunner{},
		NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel},
		os.Getenv("FAAS_TEST_FC_VERSION"),
		nil,
	)
}

// TestMetalBoot50Concurrent is the M1 headline acceptance test.
func TestMetalBoot50Concurrent(t *testing.T) {
	kernel, base, layer := metalImages(t)
	m := newMetalManager(t, kernel)
	const n = 50

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("m1-%d", i)
			if _, err := m.ColdBoot(ctx, ColdBootRequest{
				Instance: id, BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128,
			}); err != nil {
				t.Errorf("boot %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()

	if m.LiveCount() != n {
		t.Fatalf("live=%d, want %d", m.LiveCount(), n)
	}

	// Teardown; after this the box must leak nothing (`make leakcheck`).
	for i := 0; i < n; i++ {
		if err := m.Destroy(ctx, fmt.Sprintf("m1-%d", i)); err != nil {
			t.Errorf("destroy m1-%d: %v", i, err)
		}
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatalf("after teardown live=%d leased=%d, want 0/0", m.LiveCount(), m.LeasedCount())
	}
}

// TestMetalParkWakeCycle is the M3 latency gate (spec §14, V2): park→wake p50
// ≤ 350 ms over 100 cycles, restoring from a snapshot each wake.
func TestMetalParkWakeCycle(t *testing.T) {
	kernel, base, layer := metalImages(t)

	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	m := NewManager(wire.ExecRunner{}, NewJailerVMM(JailChrootBase, 30*time.Second),
		Paths{Kernel: kernel}, fcVersion, nil)

	ctx := context.Background()
	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion:   fcVersion,
		MemPath:     snapDir + "/mem",
		VMStatePath: snapDir + "/vmstate",
	}

	// Prime: cold boot once, then park to produce the first snapshot.
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: "cycle", BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	if _, err := m.Park(ctx, "cycle", SnapshotSpec{MemPath: snap.MemPath, VMStatePath: snap.VMStatePath}); err != nil {
		t.Fatalf("prime park: %v", err)
	}

	const cycles = 100
	latencies := make([]time.Duration, 0, cycles)
	for i := 0; i < cycles; i++ {
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: "cycle", BasePath: base, LayerPath: layer, VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("wake cycle %d: %v", i, err)
		}
		latencies = append(latencies, time.Since(start))
		if inst.Method != WakeRestore {
			t.Errorf("cycle %d fell back to %s — snapshot restore regressed", i, inst.Method)
		}
		if _, err := m.Park(ctx, "cycle", SnapshotSpec{MemPath: snap.MemPath, VMStatePath: snap.VMStatePath}); err != nil {
			t.Fatalf("park cycle %d: %v", i, err)
		}
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	p50 := latencies[len(latencies)/2]
	p95 := latencies[len(latencies)*95/100]
	t.Logf("wake latency over %d cycles: p50=%s p95=%s", cycles, p50, p95)
	if p50 > 350*time.Millisecond {
		t.Errorf("wake p50 = %s, want ≤ 350 ms (spec §6.3)", p50)
	}
	if p95 > 800*time.Millisecond {
		t.Errorf("wake p95 = %s, want ≤ 800 ms (spec §6.3)", p95)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases after cycles: %d", m.LeasedCount())
	}
}
