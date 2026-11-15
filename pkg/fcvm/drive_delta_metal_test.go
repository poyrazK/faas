//go:build metal

package fcvm

// adr: 20261115120000 — a parked guest's private drive is uploaded as a delta
// against its app layer, and a node without the drive restores from it.

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// remoteOnlyBackend hides the shared local backend's LocalPath, as GCS does,
// so the cache keeps its own copies and must fetch on a miss.
type remoteOnlyBackend struct{ b storage.StorageBackend }

func (r remoteOnlyBackend) Put(ctx context.Context, key string, rd io.Reader) error {
	return r.b.Put(ctx, key, rd)
}
func (r remoteOnlyBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	return r.b.Get(ctx, key)
}
func (r remoteOnlyBackend) Delete(ctx context.Context, key string) error { return r.b.Delete(ctx, key) }

// TestMetalWakeFromDriveDelta parks a real guest through a node cache that
// writes drive deltas, checks the shared store holds a delta, drops the
// drive from the cache and wakes: the restore must rebuild the drive from the
// layer and the delta. Run with TMPDIR on XFS (reflink=1).
func TestMetalWakeFromDriveDelta(t *testing.T) {
	work := t.TempDir()
	kernel, base, layerPath := memoryShareMetalImages(t, work)
	fcVersion, err := DetectFirecrackerVersion(context.Background())
	if err != nil {
		t.Fatalf("detect firecracker version: %v", err)
	}
	shared, err := storage.NewLocalStorageBackend(filepath.Join(work, "shared"))
	if err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(remoteOnlyBackend{shared}, filepath.Join(work, "cache"), 16<<30)
	if err != nil {
		t.Fatal(err)
	}
	cache.SetDriveDeltas(true)
	ctx := context.Background()
	const layerKey = "layers/drive-delta.ext4"
	f, err := os.Open(layerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, layerKey, f); err != nil {
		t.Fatalf("put layer: %v", err)
	}
	_ = f.Close()

	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(cache),
		Paths{Kernel: kernel}, fcVersion, nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	const instance = "drivedelta"
	prefix := "snap/drive-delta/captures/c1/v2/"
	snap := &Snapshot{
		FCVersion:         fcVersion,
		StorageKey:        prefix + "mem",
		VMStateStorageKey: prefix + "vmstate",
		VMStatePath:       filepath.Join(work, "vmstate"),
	}
	driveKey := state.SnapshotDriveKey(state.Snapshot{StorageKey: snap.StorageKey})
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
	}); err != nil {
		t.Fatalf("park: %v", err)
	}

	rc, err := shared.Get(ctx, driveKey)
	if err != nil {
		t.Fatalf("read uploaded drive: %v", err)
	}
	uploaded, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(uploaded, []byte("GRGDLT1\n")) {
		t.Fatalf("shared store holds the whole drive (%d bytes), not a delta", len(uploaded))
	}
	cachedPath, ok, err := cache.LocalPath(driveKey)
	if err != nil || !ok {
		t.Fatalf("cached drive: ok=%v err=%v", ok, err)
	}
	want := fileDigest(t, cachedPath)
	info, _ := os.Stat(cachedPath)
	t.Logf("drive: %d bytes logical; uploaded delta %d bytes", info.Size(), len(uploaded))

	for i := range 3 {
		if err := cache.EvictLocal(driveKey); err != nil {
			t.Fatalf("evict drive: %v", err)
		}
		if _, ok, _ := cache.LocalPath(driveKey); ok {
			t.Fatal("drive still cached after eviction")
		}
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 128, Snapshot: snap,
		})
		if err != nil {
			t.Fatalf("wake %d: %v", i, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("wake %d: woke by %s, want a snapshot restore", i, inst.Method)
		}
		t.Logf("wake %d from a delta: %s", i, time.Since(start).Round(time.Millisecond))
		rebuilt, ok, err := cache.LocalPath(driveKey)
		if err != nil || !ok {
			t.Fatalf("wake %d: drive not cached after the restore: ok=%v err=%v", i, ok, err)
		}
		if fileDigest(t, rebuilt) != want {
			t.Fatalf("wake %d: rebuilt drive differs from the captured one", i)
		}
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("wake %d: destroy: %v", i, err)
		}
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}
