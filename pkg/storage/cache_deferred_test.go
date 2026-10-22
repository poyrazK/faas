package storage_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

// gatedSync blocks every cache fsync until release is closed.
type gatedSync struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func newGatedSync() *gatedSync {
	return &gatedSync{started: make(chan struct{}, 16), release: make(chan struct{})}
}

func (g *gatedSync) sync(*os.File) error {
	g.started <- struct{}{}
	<-g.release
	return g.err
}

func newDeferredCache(t *testing.T, parent storage.StorageBackend) (*storage.LocalCacheBackend, *gatedSync) {
	t.Helper()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	gate := newGatedSync()
	cache.SetSyncFileForTest(gate.sync)
	return cache, gate
}

func getString(t *testing.T, ctx context.Context, b storage.StorageBackend, key string) (string, io.ReadCloser) {
	t.Helper()
	rc, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get %q: %v", key, err)
	}
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	if _, err := rc.(io.Seeker).Seek(0, io.SeekStart); err != nil {
		t.Fatalf("rewind %q: %v", key, err)
	}
	return string(data), rc
}

func cached(t *testing.T, cache *storage.LocalCacheBackend, key string) bool {
	t.Helper()
	_, _, ok, err := cache.LocalPathWithSource(key)
	if err != nil {
		t.Fatalf("LocalPathWithSource %q: %v", key, err)
	}
	return ok
}

func cacheTemps(t *testing.T, cache *storage.LocalCacheBackend) []string {
	t.Helper()
	var temps []string
	err := filepath.WalkDir(cache.Root(), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".faas-cache-") {
			temps = append(temps, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cache: %v", err)
	}
	return temps
}

// A wake-path miss gets the bytes while the fill is still being fsynced; the
// entry is installed only after the fsync, and a second read in between
// shares the fill instead of fetching again.
func TestLocalCacheBackend_DeferredFillReturnsBeforeSync(t *testing.T) {
	parent := newFakeBackend()
	ctx := context.Background()
	if err := parent.Put(ctx, "snap/d/mem", strings.NewReader("memory-bytes")); err != nil {
		t.Fatal(err)
	}
	cache, gate := newDeferredCache(t, parent)
	wake := storage.WithDeferredCacheDurability(ctx)

	got, rc := getString(t, wake, cache, "snap/d/mem")
	defer rc.Close()
	if got != "memory-bytes" {
		t.Fatalf("Get = %q", got)
	}
	<-gate.started
	if cached(t, cache, "snap/d/mem") {
		t.Fatal("entry installed before its fill was durable")
	}
	again, rc2 := getString(t, wake, cache, "snap/d/mem")
	defer rc2.Close()
	if again != "memory-bytes" || parent.gets.Load() != 1 {
		t.Fatalf("second Get = %q with %d parent gets, want the pending fill and 1", again, parent.gets.Load())
	}

	close(gate.release)
	cache.WaitDeferred()
	if !cached(t, cache, "snap/d/mem") {
		t.Fatal("entry not installed after its fill was durable")
	}
	if temps := cacheTemps(t, cache); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
	_, rc3 := getString(t, ctx, cache, "snap/d/mem")
	_ = rc3.Close()
	if parent.gets.Load() != 1 {
		t.Fatalf("parent gets after install = %d, want 1", parent.gets.Load())
	}
}

// Without the wake-path marker a miss still installs a durable entry before
// Get returns.
func TestLocalCacheBackend_PlainFillIsDurableOnReturn(t *testing.T) {
	parent := newFakeBackend()
	ctx := context.Background()
	if err := parent.Put(ctx, "apps/a/layer.ext4", strings.NewReader("layer")); err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	synced := 0
	cache.SetSyncFileForTest(func(f *os.File) error { synced++; return f.Sync() })
	_, rc := getString(t, ctx, cache, "apps/a/layer.ext4")
	defer rc.Close()
	if synced != 1 || !cached(t, cache, "apps/a/layer.ext4") {
		t.Fatalf("synced=%d cached=%v, want a durable entry on return", synced, cached(t, cache, "apps/a/layer.ext4"))
	}
}

// WithDurableCacheFill overrides a wake context's deferral: the entry is
// installed when Get returns, before the caller reads or even closes the
// stream, so its local path resolves at once (ADR-510's fetch of an evicted
// backing image relies on this).
func TestLocalCacheBackend_DurableFillOverridesDeferral(t *testing.T) {
	parent := newFakeBackend()
	ctx := context.Background()
	if err := parent.Put(ctx, "base/releases/runner.ext4", strings.NewReader("base")); err != nil {
		t.Fatal(err)
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cache.Get(storage.WithDurableCacheFill(storage.WithDeferredCacheDurability(ctx)), "base/releases/runner.ext4")
	if err != nil {
		t.Fatal(err)
	}
	if !cached(t, cache, "base/releases/runner.ext4") {
		t.Fatal("entry not installed when Get returned")
	}
	if err := rc.Close(); err != nil {
		t.Fatal(err)
	}
	if !cached(t, cache, "base/releases/runner.ext4") {
		t.Fatal("entry lost when the unread stream was closed")
	}
}

// A fill whose fsync fails is never installed and leaves no temp file; the
// caller's bytes stay valid and the next miss fetches again.
func TestLocalCacheBackend_DeferredFillSyncFailureIsNotCached(t *testing.T) {
	parent := newFakeBackend()
	ctx := context.Background()
	if err := parent.Put(ctx, "snap/d/drive", strings.NewReader("drive-bytes")); err != nil {
		t.Fatal(err)
	}
	cache, gate := newDeferredCache(t, parent)
	gate.err = errors.New("EIO")
	wake := storage.WithDeferredCacheDurability(ctx)

	got, rc := getString(t, wake, cache, "snap/d/drive")
	defer rc.Close()
	close(gate.release)
	cache.WaitDeferred()
	if got != "drive-bytes" {
		t.Fatalf("Get = %q", got)
	}
	if cached(t, cache, "snap/d/drive") {
		t.Fatal("fill installed after its fsync failed")
	}
	if temps := cacheTemps(t, cache); len(temps) != 0 {
		t.Fatalf("temp files left: %v", temps)
	}
	if data, err := io.ReadAll(rc); err != nil || string(data) != "drive-bytes" {
		t.Fatalf("caller's reader after a failed install: %q, %v", data, err)
	}
	cache.SetSyncFileForTest((*os.File).Sync)
	_, rc2 := getString(t, ctx, cache, "snap/d/drive")
	_ = rc2.Close()
	if parent.gets.Load() != 2 {
		t.Fatalf("parent gets = %d, want a second fetch", parent.gets.Load())
	}
}

// A caller can retain a deferred fill by hard link both before and after it
// is installed; vmmd stages restore inputs this way.
func TestLocalCacheBackend_DeferredFillLinksBeforeAndAfterInstall(t *testing.T) {
	parent := newFakeBackend()
	ctx := context.Background()
	if err := parent.Put(ctx, "snap/d/mem", strings.NewReader("linked-bytes")); err != nil {
		t.Fatal(err)
	}
	cache, gate := newDeferredCache(t, parent)
	wake := storage.WithDeferredCacheDurability(ctx)
	// Hard links need the cache's filesystem.
	dir := filepath.Join(filepath.Dir(cache.Root()), "staged")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	_, before := getString(t, wake, cache, "snap/d/mem")
	defer before.Close()
	_, after := getString(t, wake, cache, "snap/d/mem")
	defer after.Close()
	<-gate.started
	link := func(rc io.ReadCloser, name string) {
		t.Helper()
		linker, ok := rc.(storage.LocalFileLinker)
		if !ok {
			t.Fatalf("%T is not a LocalFileLinker", rc)
		}
		if err := linker.LinkTo(filepath.Join(dir, name)); err != nil {
			t.Fatalf("LinkTo %s: %v", name, err)
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "linked-bytes" {
			t.Fatalf("linked %s = %q, %v", name, data, err)
		}
	}
	link(before, "before")
	close(gate.release)
	cache.WaitDeferred()
	link(after, "after")
	entry, _, ok, err := cache.LocalPathWithSource("snap/d/mem")
	if err != nil || !ok {
		t.Fatalf("entry not installed: %v", err)
	}
	for _, name := range []string{"before", "after"} {
		a, _ := os.Stat(filepath.Join(dir, name))
		b, _ := os.Stat(entry)
		if !os.SameFile(a, b) {
			t.Fatalf("%s link is not the installed entry", name)
		}
	}
}

// Delete and Refresh supersede a fill that is still being made durable.
func TestLocalCacheBackend_DeferredFillSuperseded(t *testing.T) {
	ctx := context.Background()
	wake := storage.WithDeferredCacheDurability(ctx)
	for _, tc := range []struct {
		name string
		act  func(*storage.LocalCacheBackend, *fakeBackend) error
		want string // installed content afterwards, "" for none
	}{
		{"delete", func(c *storage.LocalCacheBackend, _ *fakeBackend) error {
			return c.Delete(ctx, "snap/d/mem")
		}, ""},
		{"refresh", func(c *storage.LocalCacheBackend, p *fakeBackend) error {
			p.blobs["snap/d/mem"] = []byte("v2")
			rc, err := c.Refresh(ctx, "snap/d/mem")
			if err == nil {
				err = rc.Close()
			}
			return err
		}, "v2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := newFakeBackend()
			parent.blobs["snap/d/mem"] = []byte("v1")
			cache, gate := newDeferredCache(t, parent)
			_, rc := getString(t, wake, cache, "snap/d/mem")
			defer rc.Close()
			<-gate.started
			done := make(chan error, 1)
			go func() { done <- tc.act(cache, parent) }()
			if tc.name == "refresh" {
				<-gate.started // the refresh's own durable fsync
			}
			close(gate.release)
			if err := <-done; err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			cache.WaitDeferred()
			path, _, ok, _ := cache.LocalPathWithSource("snap/d/mem")
			got := ""
			if ok {
				data, _ := os.ReadFile(path)
				got = string(data)
			}
			if got != tc.want {
				t.Fatalf("installed after %s = %q, want %q", tc.name, got, tc.want)
			}
			if temps := cacheTemps(t, cache); len(temps) != 0 {
				t.Fatalf("temp files left: %v", temps)
			}
		})
	}
}

// Below api.ArtifactCacheMinFreeBytes free, the cache evicts its oldest
// entries even under its byte budget, but never the newest one.
func TestLocalCacheBackend_LowDiskEvictsOldestKeepsNewest(t *testing.T) {
	ctx := context.Background()
	epoch := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		free int64
		want []bool
	}{
		{"plenty free", api.ArtifactCacheMinFreeBytes * 4, []bool{true, true, true}},
		{"low disk", api.ArtifactCacheMinFreeBytes / 2, []bool{false, false, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := newFakeBackend()
			keys := []string{"apps/a/1.ext4", "apps/a/2.ext4", "apps/a/3.ext4"}
			cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
			if err != nil {
				t.Fatal(err)
			}
			cache.SetMinFreeBytes(api.ArtifactCacheMinFreeBytes)
			cache.SetDiskSpaceForTest(func(string) (int64, int64, error) {
				return tc.free, 100 << 30, nil
			})
			for i, k := range keys {
				parent.blobs[k] = []byte("layer " + k)
				_, rc := getString(t, ctx, cache, k)
				_ = rc.Close()
				// Each entry is older than the next install. CacheFileForKey
				// does not queue an access touch the way a cache read does.
				path := storage.CacheFileForKey(cache.Root(), k)
				if _, err := os.Stat(path); err == nil {
					when := epoch.Add(time.Duration(i) * time.Minute)
					if err := os.Chtimes(path, when, when); err != nil {
						t.Fatal(err)
					}
				}
			}
			for i, k := range keys {
				if got := cached(t, cache, k); got != tc.want[i] {
					t.Errorf("%s cached = %v, want %v", k, got, tc.want[i])
				}
			}
		})
	}
}
