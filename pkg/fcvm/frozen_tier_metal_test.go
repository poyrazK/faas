//go:build metal

// adr: 1151 — the production frozen-tier path on a restored guest: freeze
// without reclaim while quiet pause runs, refuse a busy guest and snapshots
// of a frozen one, thaw to a serving guest, destroy while frozen, no leaks.
package fcvm

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalFrozenTier freezes and thaws a guest restored from a snapshot the
// way schedd and vmmd do in production. FAAS_TEST_FROZEN_CYCLES sets the
// number of freeze/thaw cycles (default 20).
func TestMetalFrozenTier(t *testing.T) {
	kernel, base, layer := metalImages(t)
	cycles := envInt(t, "FAAS_TEST_FROZEN_CYCLES", 20)
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
	tracker := activity.NewWithDefaults()
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(store),
		Paths{Kernel: kernel}, fcVersion, nil, nil).
		WithQuietPause(QuietPauseConfig{Activity: tracker, Tick: 20 * time.Millisecond}).
		WithFreeze(FreezeConfig{Activity: tracker})
	withCgroupRootAt(t, "/sys/fs/cgroup")
	pauseCtx, stopPause := context.WithCancel(ctx)
	defer stopPause()
	go m.RunQuietPause(pauseCtx)

	const id = "frozen-tier"
	snapDir := t.TempDir()
	snap := &Snapshot{
		FCVersion: fcVersion, StorageKey: "snap/frozen-tier/mem", VMStateStorageKey: "snap/frozen-tier/vmstate",
		VMStatePath: snapDir + "/vmstate",
	}
	spec := SnapshotSpec{VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey}
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 256,
	}); err != nil {
		t.Fatalf("prime cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
	if _, err := m.Park(ctx, id, spec); err != nil {
		t.Fatalf("prime park: %v", err)
	}
	inst, err := m.Wake(ctx, WakeRequest{Instance: id, Plan: "scale", BaseKey: base, LayerKey: layer, VcpuCount: 2,
		MemSizeMiB: 256, Snapshot: snap, QuietPauseAfter: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("restore wake: %v", err)
	}
	if inst.Method != WakeRestore {
		t.Fatalf("wake fell back to %s; the test needs a restored guest", inst.Method)
	}
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"
	client := &http.Client{Timeout: 2 * time.Second}
	// request is what vmmd's forward RPCs do: mark activity, wait out a
	// freeze, resume a quiet pause, send the request.
	request := func() error {
		tracker.Begin(id)
		defer tracker.End(id)
		if frozen, err := m.AwaitFrozen(ctx, id); err != nil || frozen {
			return errors.Join(errors.New("guest is frozen"), err)
		}
		if err := m.QuietResume(ctx, id); err != nil {
			return err
		}
		resp, err := client.Get(url)
		if err != nil {
			return err
		}
		return resp.Body.Close()
	}
	serve := func(within time.Duration) error {
		deadline := time.Now().Add(within)
		for {
			err := request()
			if err == nil || time.Now().After(deadline) {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if err := serve(5 * time.Second); err != nil {
		t.Fatalf("restored guest does not serve: %v", err)
	}

	// A guest with a request in flight is never frozen.
	tracker.Begin(id)
	if _, err := m.Freeze(ctx, id, FreezeOptions{}); !errors.Is(err, ErrInstanceBusy) {
		t.Fatalf("freeze with a request in flight: %v, want ErrInstanceBusy", err)
	}
	tracker.End(id)

	var thaws, firstBytes []time.Duration
	probe := &http.Client{Timeout: 300 * time.Millisecond}
	for cycle := range cycles {
		// Let quiet pause pause the idle guest first, as in production.
		time.Sleep(250 * time.Millisecond)
		if _, err := m.Freeze(ctx, id, FreezeOptions{}); err != nil {
			t.Fatalf("freeze %d: %v", cycle, err)
		}
		if frozen, _ := m.FrozenState(id); !frozen {
			t.Fatalf("freeze %d: not reported frozen", cycle)
		}
		if err := request(); err == nil {
			t.Fatalf("freeze %d: a forward reached the frozen guest", cycle)
		}
		if cycle == 0 {
			if _, err := m.Park(ctx, id, spec); !errors.Is(err, ErrInstanceFrozen) {
				t.Fatalf("park of a frozen guest: %v, want ErrInstanceFrozen", err)
			}
			// Quiet pause ticks every 20 ms; none may resume the guest.
			time.Sleep(time.Second)
			if resp, err := probe.Get(url); err == nil {
				_ = resp.Body.Close()
				t.Fatal("the frozen guest answered: quiet pause resumed it")
			}
		}
		started := time.Now()
		if err := m.Thaw(ctx, id); err != nil {
			t.Fatalf("thaw %d: %v", cycle, err)
		}
		thaws = append(thaws, time.Since(started))
		if err := serve(2 * time.Second); err != nil {
			t.Fatalf("thaw %d: guest does not serve: %v", cycle, err)
		}
		firstBytes = append(firstBytes, time.Since(started))
	}
	slices.Sort(thaws)
	slices.Sort(firstBytes)
	t.Logf("%d cycles: thaw p50=%s max=%s; thaw+response p50=%s max=%s", cycles,
		thaws[len(thaws)/2], thaws[len(thaws)-1], firstBytes[len(firstBytes)/2], firstBytes[len(firstBytes)-1])

	// schedd destroys a frozen VM rather than snapshotting it.
	if _, err := m.Freeze(ctx, id, FreezeOptions{}); err != nil {
		t.Fatalf("final freeze: %v", err)
	}
	if err := m.Destroy(ctx, id); err != nil {
		t.Fatalf("destroy a frozen guest: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Fatalf("leases after destroy = %d, want 0", m.LeasedCount())
	}
}
