package storage_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

// ADR-20261115140000: on XFS the free count lags unlinks (background inode
// GC, reflink-shared extents), so a reclaim that waited for statfs to agree
// evicted the whole cache for a small shortfall. It now stops once the bytes
// it removed cover the shortfall it measured.
func TestLocalCacheBackend_ReclaimStopsAtTheShortfallWhenStatfsLags(t *testing.T) {
	ctx := context.Background()
	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	const entry = 1 << 20
	const free = int64(10 << 30)
	// The filesystem never reports the removals.
	cache.SetDiskSpaceForTest(func(string) (int64, int64, error) { return free, 100 << 30, nil })
	keys := []string{"snap/b/1", "snap/b/2", "snap/b/3", "snap/b/4", "snap/b/5"}
	for i, k := range keys {
		parent.blobs[k] = bytes.Repeat([]byte{byte(i + 1)}, entry)
		_, rc := getString(t, ctx, cache, k)
		_ = rc.Close()
		when := epoch.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(storage.CacheFileForKey(cache.Root(), k), when, when); err != nil {
			t.Fatal(err)
		}
	}
	// Short by two entries.
	if err := storage.ReclaimFreeBytes(cache, free+2*entry); err != nil {
		t.Fatal(err)
	}
	for i, want := range []bool{false, false, true, true, true} {
		if got := cached(t, cache, keys[i]); got != want {
			t.Errorf("%s cached = %v, want %v (evict the shortfall, oldest first)", keys[i], got, want)
		}
	}
}
