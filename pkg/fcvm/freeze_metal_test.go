//go:build metal

// adr: 906 — freeze/thaw keeps a restored guest serving, reachable only
// while thawed, on host time after the pause, and leaves no leases behind.
package fcvm

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalFreezeThawCycle restores a guest from a snapshot, as a woken
// app is in production, then freezes and thaws it. It logs the memory
// footprint and thaw-to-first-byte latency next to a park+wake cycle of
// the same guest. FAAS_TEST_FREEZE_HOLD sets how long each freeze lasts
// (default 3s); FAAS_TEST_FREEZE_MEM_MIB sets the guest size (default 256).
func TestMetalFreezeThawCycle(t *testing.T) {
	kernel, base, layer := metalImages(t)
	hold := 3 * time.Second
	if raw := os.Getenv("FAAS_TEST_FREEZE_HOLD"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			t.Fatalf("FAAS_TEST_FREEZE_HOLD=%q: %v", raw, err)
		}
		hold = d
	}
	memMiB := 256
	if raw := os.Getenv("FAAS_TEST_FREEZE_MEM_MIB"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 128 {
			t.Fatalf("FAAS_TEST_FREEZE_MEM_MIB=%q must be an integer >= 128", raw)
		}
		memMiB = n
	}
	ctx := context.Background()
	fcVersion, err := DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	snapshotRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(snapshotRoot, "snap"), 0o2770); err != nil {
		t.Fatalf("create snapshot root: %v", err)
	}
	store, err := storage.NewLocalStorageBackend(snapshotRoot)
	if err != nil {
		t.Fatalf("create snapshot storage: %v", err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	const id = "frozen"
	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        "snap/frozen/mem",
		VMStateStorageKey: "snap/frozen/vmstate",
		VMStatePath:       snapDir + "/vmstate",
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	wake := WakeRequest{Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB, Snapshot: snap}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: memMiB,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	if _, err := m.Park(ctx, id, spec); err != nil {
		t.Fatalf("prime park: %v", err)
	}

	// Reference: park + snapshot wake to first byte.
	started := time.Now()
	inst, err := m.Wake(ctx, wake)
	if err != nil {
		t.Fatalf("restore wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("wake fell back to %s; the test needs a restored guest", inst.Method)
	}
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	if _, err := firstByte(url, 5*time.Second); err != nil {
		t.Fatalf("restored guest does not serve: %v", err)
	}
	wakeToByte := time.Since(started)

	for cycle := 0; cycle < 3; cycle++ {
		rep, err := m.Freeze(ctx, id, FreezeOptions{Reclaim: true})
		if err != nil {
			t.Fatalf("freeze %d: %v", cycle, err)
		}
		if rep.AfterBytes > rep.BeforeBytes {
			t.Errorf("freeze %d: charge grew across reclaim: %+v", cycle, rep)
		}
		if frozen, since := m.FrozenState(id); !frozen || since.IsZero() {
			t.Fatalf("freeze %d: FrozenState = %v, %v", cycle, frozen, since)
		}
		if _, err := (&http.Client{Timeout: 300 * time.Millisecond}).Get(url); err == nil {
			t.Fatalf("freeze %d: paused guest answered HTTP", cycle)
		}
		time.Sleep(hold)
		started := time.Now()
		if err := m.Thaw(ctx, id); err != nil {
			t.Fatalf("thaw %d: %v", cycle, err)
		}
		resumed := time.Since(started)
		if frozen, _ := m.FrozenState(id); frozen {
			t.Fatalf("thaw %d: instance still reported frozen", cycle)
		}
		resp, err := firstByte(url, 5*time.Second)
		if err != nil {
			t.Fatalf("thaw %d: guest does not serve: %v", cycle, err)
		}
		toByte := time.Since(started)
		// kvm-clock follows host time, so the guest clock must not lag by
		// the time it spent paused. busybox httpd reports it in Date.
		if date, perr := http.ParseTime(resp.Header.Get("Date")); perr == nil {
			if lag := time.Since(date); lag > 2*time.Second || lag < -2*time.Second {
				t.Errorf("thaw %d: guest clock off by %s after a %s freeze", cycle, lag, hold)
			}
		}
		t.Logf("freeze %d: pause=%s reclaim=%s charge %d→%d MiB swapped=%d MiB; hold=%s thaw=%s first_byte=%s",
			cycle, rep.Pause, rep.Reclaim, rep.BeforeBytes>>20, rep.AfterBytes>>20, rep.SwappedBytes>>20,
			hold, resumed, toByte)
	}
	t.Logf("reference snapshot wake to first byte: %s", wakeToByte)

	if err := m.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}

// firstByte polls url until the guest returns a response, and reads one
// byte of its body.
func firstByte(url string, within time.Duration) (*http.Response, error) {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(within)
	for {
		resp, err := client.Get(url)
		if err == nil {
			_, _ = io.ReadFull(resp.Body, make([]byte, 1))
			_ = resp.Body.Close()
			return resp, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(2 * time.Millisecond)
	}
}
