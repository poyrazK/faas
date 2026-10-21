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

	"github.com/onebox-faas/faas/pkg/storage"
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
