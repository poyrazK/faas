package storage_test

// adr: 20261114120000

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// TestResolveCacheBudget sizes the cache from its disk: the disk less a 15%
// reserve (never under the 8 GiB floor), which is also the free-space floor.
// RAM still sets the budget when the disk is small or cannot be measured.
func TestResolveCacheBudget(t *testing.T) {
	const gib = int64(1) << 30
	for _, tc := range []struct {
		name               string
		mem, fs            int64
		wantMax, wantFloor int64
	}{
		{"100 GiB node disk", 16 * gib, 100 * gib, 85 * gib, 15 * gib},
		{"1.5 TiB disk", 64 * gib, 1536 * gib, 1536*gib - 1536*gib/100*15, 1536 * gib / 100 * 15},
		{"small disk keeps the 8 GiB floor and the RAM budget", 16 * gib, 12 * gib, 8 * gib, 8 * gib},
		{"unmeasured disk keeps the RAM budget", 16 * gib, 0, 8 * gib, api.ArtifactCacheMinFreeBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gotMax, gotFloor := storage.ResolveCacheBudget(tc.mem, tc.fs)
			if gotMax != tc.wantMax || gotFloor != tc.wantFloor {
				t.Errorf("ResolveCacheBudget(%d GiB, %d GiB) = (%d, %d), want (%d, %d)",
					tc.mem/gib, tc.fs/gib, gotMax, gotFloor, tc.wantMax, tc.wantFloor)
			}
		})
	}
}

// TestEvictionTiersOrderEviction: when the disk runs low, an entry nothing
// references goes first even if it is the newest, then ordinary entries, and
// a live deployment's snapshot last even if it is the oldest.
func TestEvictionTiersOrderEviction(t *testing.T) {
	ctx := context.Background()
	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	const (
		live   = "snap/d/captures/c1/v2/mem"
		normal = "apps/a/layer.ext4"
		unused = "base/releases/runner-node22-amd64-old.ext4"
	)
	for _, tc := range []struct {
		name    string
		reclaim int
		want    map[string]bool
	}{
		{"one eviction takes the unused entry", 1, map[string]bool{live: true, normal: true, unused: false}},
		{"two take the ordinary entry next", 2, map[string]bool{live: true, normal: false, unused: false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := newFakeBackend()
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
			if err != nil {
				t.Fatal(err)
			}
			keys := []string{live, normal, unused} // oldest first
			for i, k := range keys {
				parent.blobs[k] = []byte("blob " + k)
				_, rc := getString(t, ctx, cache, k)
				_ = rc.Close()
				when := epoch.Add(time.Duration(i) * time.Minute)
				if err := os.Chtimes(storage.CacheFileForKey(cache.Root(), k), when, when); err != nil {
					t.Fatal(err)
				}
			}
			// Each eviction frees one unit.
			const unit = int64(1) << 30
			cache.SetDiskSpaceForTest(func(string) (int64, int64, error) {
				var gone int64
				for _, k := range keys {
					if _, err := os.Stat(storage.CacheFileForKey(cache.Root(), k)); err != nil {
						gone++
					}
				}
				return gone * unit, 100 * unit, nil
			})
			cache.SetEvictionTier(func(key string) int {
				switch key {
				case live:
					return storage.EvictLast
				case unused:
					return storage.EvictFirst
				}
				return storage.EvictNormal
			})
			if err := cache.ReclaimFreeBytes(int64(tc.reclaim) * unit); err != nil {
				t.Fatal(err)
			}
			for k, want := range tc.want {
				if got := cached(t, cache, k); got != want {
					t.Errorf("%s cached = %v, want %v", k, got, want)
				}
			}
		})
	}
}

// TestLocalEntriesAndEvictLocal: the sweep lists what this disk holds and
// removes one entry without touching the parent's copy.
func TestLocalEntriesAndEvictLocal(t *testing.T) {
	ctx := context.Background()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	const key = "snap/d/captures/c1/v2/drive"
	parent.blobs[key] = []byte("drive")
	_, rc := getString(t, ctx, cache, key)
	_ = rc.Close()
	entries, err := cache.LocalEntries()
	if err != nil || len(entries) != 1 || entries[0].Key != key {
		t.Fatalf("LocalEntries = %+v, %v; want one entry for %s", entries, err, key)
	}
	if err := cache.EvictLocal(key); err != nil {
		t.Fatal(err)
	}
	if cached(t, cache, key) {
		t.Fatal("entry still cached after EvictLocal")
	}
	if _, ok := parent.blobs[key]; !ok {
		t.Fatal("EvictLocal removed the parent's copy")
	}
}
