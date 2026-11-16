//go:build metal

package fcvm

// adr: 20261110210000 — slice 3d-3: park and warm snapshots are sealed at
// the source. Only ciphertext reaches the shared store and the node cache,
// a sealed park snapshot restores, and one whose key cannot be opened is
// cache: the wake cold-boots instead of failing.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/crashcrypt"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalWakeFromSealedParkSnapshot(t *testing.T) {
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
	ctx := context.Background()
	const layerKey = "layers/sealed-park.ext4"
	f, err := os.Open(layerPath) //nolint:forbidigo // test fixture.
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.Put(ctx, layerKey, f); err != nil {
		t.Fatalf("put layer: %v", err)
	}
	_ = f.Close()

	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 30*time.Second).WithStorage(cache),
		Paths{Kernel: kernel}, fcVersion, nil, nil)
	m.WithStorage(cache) // as vmmd wires it: SealCaptureStorage wraps the manager's store
	m.SetHostIdentities([]*age.X25519Identity{fleet})
	withCgroupRootAt(t, "/sys/fs/cgroup")

	const instance, captureID = "sealedpark", "5b0c3f9e-7d2a-4c1e-9a43-0f6d2b8e1a11"
	prefix := "snap/sealed-park/captures/" + captureID + "/v2/"
	memKey, vmstateKey := prefix+"mem", prefix+"vmstate"
	if _, err := m.ColdBoot(ctx, ColdBootRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 128,
	}); err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })

	store, sealedKey, err := m.SealCaptureStorage(captureID)
	if err != nil {
		t.Fatalf("seal capture storage: %v", err)
	}
	if _, err := m.Park(ctx, instance, SnapshotSpec{
		VMStatePath: filepath.Join(work, "vmstate"), StorageKey: memKey, VMStateStorageKey: vmstateKey, Storage: store,
	}); err != nil {
		t.Fatalf("park: %v", err)
	}

	// Only ciphertext left the guest: no plaintext object in the shared
	// store or the node cache, and the encrypted twins exist.
	for _, key := range []string{memKey, vmstateKey} {
		if rc, err := shared.Get(ctx, key); err == nil {
			_ = rc.Close()
			t.Fatalf("shared store holds plaintext %s", key)
		}
		if _, ok, _ := cache.LocalPath(key); ok {
			t.Fatalf("node cache holds plaintext %s", key)
		}
		rc, err := shared.Get(ctx, key+crashcrypt.Suffix)
		if err != nil {
			t.Fatalf("encrypted twin %s%s missing: %v", key, crashcrypt.Suffix, err)
		}
		_ = rc.Close()
	}

	snap := func(key []byte) *Snapshot {
		return &Snapshot{
			FCVersion: fcVersion, StorageKey: memKey, VMStateStorageKey: vmstateKey,
			VMStatePath: filepath.Join(work, "vmstate"),
			CaptureID:   captureID, SealedKey: key, SealedCache: true,
		}
	}
	for i := range 2 {
		start := time.Now()
		inst, err := m.Wake(ctx, WakeRequest{
			Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 128,
			Snapshot: snap(sealedKey),
		})
		if err != nil {
			t.Fatalf("wake %d from the sealed snapshot: %v", i, err)
		}
		if inst.Method != WakeRestore {
			t.Fatalf("wake %d: woke by %s, want a restore of the sealed snapshot", i, inst.Method)
		}
		t.Logf("wake %d from a sealed park snapshot: %s", i, time.Since(start).Round(time.Millisecond))
		if err := m.Destroy(ctx, instance); err != nil {
			t.Fatalf("wake %d: destroy: %v", i, err)
		}
	}

	// A key that does not open is a cache miss for a park snapshot: the
	// wake cold-boots (ADR-005), unlike a crash capture, which fails.
	other, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	_, foreign, err := crashcrypt.NewKey(other.Recipient(), captureID)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := m.Wake(ctx, WakeRequest{
		Instance: instance, Plan: "pro", BaseKey: base, LayerKey: layerKey, VcpuCount: 2, MemSizeMiB: 128,
		Snapshot: snap(foreign),
	})
	if err != nil {
		t.Fatalf("wake with an unopenable sealed key must cold-boot, got error: %v", err)
	}
	if inst.Method != WakeColdBoot {
		t.Fatalf("wake with an unopenable sealed key: woke by %s, want a cold boot", inst.Method)
	}
	if err := m.Destroy(ctx, instance); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("destroy: %v", err)
	}
	if m.LeasedCount() != 0 {
		t.Errorf("leaked leases: %d", m.LeasedCount())
	}
}
