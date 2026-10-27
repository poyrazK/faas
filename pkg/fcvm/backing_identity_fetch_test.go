// adr: 510 — a restore whose base was evicted from the node cache fetches it
// and still verifies the capture's identity before attaching it.
package fcvm

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"
)

// uncachedParent hides the canonical store's local paths, as a remote
// registry or bucket does, so the node cache is the only local copy.
type uncachedParent struct{ storage.StorageBackend }

func TestVerifySnapshotBackingFetchesEvictedBase(t *testing.T) {
	const baseKey = "base/releases/runner-go-amd64-test.ext4"
	for _, tc := range []struct {
		name    string
		replace bool
		want    error
	}{
		{name: "evicted base is fetched and the capture restores", want: nil},
		{name: "fetched base that differs is still refused", replace: true, want: ErrSnapshotBackingChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			kernel := filepath.Join(dir, "vmlinux")
			writeFile(t, kernel, "kernel-v1")
			parent, err := storage.NewLocalStorageBackend(filepath.Join(dir, "store"))
			if err != nil {
				t.Fatal(err)
			}
			if err := parent.Put(ctx, baseKey, bytes.NewReader([]byte("base-layout-a"))); err != nil {
				t.Fatal(err)
			}
			cacheDir := filepath.Join(dir, "cache")
			cache, err := storage.NewLocalCacheBackend(uncachedParent{parent}, cacheDir, 1<<30)
			if err != nil {
				t.Fatal(err)
			}
			m := NewManager(&fakeRunner{}, &fakeVMM{}, Paths{Kernel: kernel}, testFCVersion, nil, nil)
			m.WithStorage(cache)
			// The VM booted with the base in the cache, and its capture recorded it.
			if _, err := m.fetchBackingFile(ctx, baseKey); err != nil {
				t.Fatal(err)
			}
			m.rememberInstanceBacking("i", baseKey)
			memKey := "snap/d1/captures/c1/v2/mem"
			m.writeSnapshotBacking(ctx, "i", memKey)
			snap := &Snapshot{FCVersion: testFCVersion, StorageKey: memKey, VMStatePath: "/snap/state"}
			// The cache evicts the base before the next wake.
			cached := storage.CacheFileForKey(cacheDir, baseKey)
			for _, p := range []string{cached, cached + ".meta"} {
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.resolveBackingFile(baseKey); !errors.Is(err, errBackingNotCached) {
				t.Fatalf("fixture: evicted base resolves: %v", err)
			}
			if tc.replace {
				if err := parent.Put(ctx, baseKey, bytes.NewReader([]byte("base-layout-b"))); err != nil {
					t.Fatal(err)
				}
			}
			err = m.verifySnapshotBacking(ctx, snap, baseKey)
			if tc.want == nil && err != nil {
				t.Fatalf("restore refused after fetching the evicted base: %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("verify = %v, want %v", err, tc.want)
			}
			if _, err := os.Stat(cached); err != nil {
				t.Fatalf("fetched base was not installed in the cache: %v", err)
			}
		})
	}
}

// wakeFetchFixture is a storage-backed Manager whose base is a logical key
// served through the real node cache over a canonical store with no local
// paths, as production vmmd's cache over GCS is.
type wakeFetchFixture struct {
	m        *Manager
	vmm      *fakeVMM
	parent   *storage.LocalStorageBackend
	cacheDir string
	layer    string
}

func newWakeFetchFixture(ctx context.Context, t *testing.T, baseKey string) wakeFetchFixture {
	t.Helper()
	dir := t.TempDir()
	kernel := filepath.Join(dir, "vmlinux")
	writeFile(t, kernel, "kernel-v1")
	parent, err := storage.NewLocalStorageBackend(filepath.Join(dir, "store"))
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Put(ctx, baseKey, bytes.NewReader([]byte("base-layout-a"))); err != nil {
		t.Fatal(err)
	}
	clean := []byte(`{"image":"test","findings":{"CRITICAL":0}}`)
	if err := parent.Put(ctx, wire.ScanKeyForBaseKey(baseKey), bytes.NewReader(clean)); err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(dir, "cache")
	cache, err := storage.NewLocalCacheBackend(uncachedParent{parent}, cacheDir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	vmm := &fakeVMM{}
	m := NewManager(&fakeRunner{}, vmm, Paths{Kernel: kernel}, testFCVersion, nil, nil)
	m.WithStorage(cache)
	return wakeFetchFixture{m: m, vmm: vmm, parent: parent, cacheDir: cacheDir, layer: filepath.Join(dir, "layer.ext4")}
}

// TestWakeAfterBaseEviction drives Manager.Wake: a capture whose base was
// evicted from the node cache restores after fetching it, and a fetched base
// whose identity differs from the capture's still cold-boots without the
// snapshot's RAM ever being loaded.
func TestWakeAfterBaseEviction(t *testing.T) {
	const baseKey = "base/releases/runner-go-amd64-wake.ext4"
	for _, tc := range []struct {
		name        string
		replace     bool
		want        WakeMethod
		wantRestore int
		wantBoots   int
	}{
		{name: "evicted base restores", want: WakeRestore, wantRestore: 1},
		{name: "changed base cold-boots", replace: true, want: WakeColdBoot, wantBoots: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			f := newWakeFetchFixture(ctx, t, baseKey)
			if _, err := f.m.fetchBackingFile(ctx, baseKey); err != nil {
				t.Fatal(err)
			}
			f.m.rememberInstanceBacking("source", baseKey)
			memKey := "snap/d1/captures/c1/v2/mem"
			f.m.writeSnapshotBacking(ctx, "source", memKey)
			cached := storage.CacheFileForKey(f.cacheDir, baseKey)
			for _, p := range []string{cached, cached + ".meta"} {
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			}
			if tc.replace {
				if err := f.parent.Put(ctx, baseKey, bytes.NewReader([]byte("base-layout-b"))); err != nil {
					t.Fatal(err)
				}
			}
			inst, err := f.m.Wake(ctx, WakeRequest{
				Instance: "restore-A", BaseKey: baseKey, LayerKey: f.layer,
				VcpuCount: 2, MemSizeMiB: 128, Plan: api.PlanHobby,
				Snapshot: &Snapshot{FCVersion: testFCVersion, StorageKey: memKey, VMStatePath: "/snap/state"},
			})
			if err != nil {
				t.Fatalf("Wake: %v", err)
			}
			if inst.Method != tc.want {
				t.Fatalf("method = %s, want %s", inst.Method, tc.want)
			}
			f.vmm.mu.Lock()
			restored, boots := len(f.vmm.restored), f.vmm.bootCount
			f.vmm.mu.Unlock()
			if restored != tc.wantRestore || boots != tc.wantBoots {
				t.Fatalf("restored=%d bootCount=%d, want %d and %d", restored, boots, tc.wantRestore, tc.wantBoots)
			}
			if _, err := os.Stat(cached); err != nil {
				t.Fatalf("base not back in the node cache: %v", err)
			}
		})
	}
}
