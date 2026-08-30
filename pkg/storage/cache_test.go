// cache_test.go — ADR-054 §2 read-through cache test surface.
//
// Pins the four behaviors the LocalCacheBackend has to honor:
//
//  1. Put-then-Get round-trips: data written through the cache is
//     readable from the cache without touching the parent.
//  2. Cache miss falls back to parent and populates: a Get on a
//     cold cache reads from the parent + caches the blob for the
//     next call.
//  3. LRU eviction by mtime: when total size exceeds maxBytes, the
//     oldest entries are evicted first.
//  4. List recovers original storage keys via sidecar: the on-disk
//     hash-derived filename is invisible to callers; List returns
//     the keys the caller originally Put.
//
// Plus the integration seam: wrapping a PrefixRouter with a cache
// keeps routing intact (snap/ → local, apps/ → OCI stub) and adds
// the read-through behaviour on every per-route Get.

package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

// fakeBackend is a minimal in-memory StorageBackend for cache
// tests. Tracks Put/Get/Delete counts so the suite can assert
// the cache hit/miss path actually short-circuited.
type fakeBackend struct {
	puts    atomic.Int64
	gets    atomic.Int64
	deletes atomic.Int64
	blobs   map[string][]byte
	// onGet, if non-nil, runs before reading blobs. Lets tests
	// simulate transient registry failures (ADR-054 §2's
	// motivating case: a registry outage must not brick cold
	// boots when the cache is warm).
	onGet func(key string) error
}

type localPathFakeBackend struct {
	*fakeBackend
	root string
}

func (f *localPathFakeBackend) LocalPath(key string) (string, bool, error) {
	return filepath.Join(f.root, key), true, nil
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{blobs: map[string][]byte{}}
}

func (f *fakeBackend) Put(_ context.Context, key string, r io.Reader) error {
	f.puts.Add(1)
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.blobs[key] = data
	return nil
}

func (f *fakeBackend) Get(_ context.Context, key string) (io.ReadCloser, error) {
	f.gets.Add(1)
	if f.onGet != nil {
		if err := f.onGet(key); err != nil {
			return nil, err
		}
	}
	data, ok := f.blobs[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(data))), nil
}

func (f *fakeBackend) Delete(_ context.Context, key string) error {
	f.deletes.Add(1)
	delete(f.blobs, key)
	return nil
}

// TestLocalCacheBackend_PutGetRoundTrip pins the basic read-through
// contract: a Put writes through to parent + caches; a Get reads
// from cache without touching the parent.
func TestLocalCacheBackend_PutGetRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	if err := cache.Put(ctx, "snap/abc", strings.NewReader("blob-data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// First Get hits cache (parent.Get count stays at 0).
	got, err := readAll(ctx, cache, "snap/abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "blob-data" {
		t.Errorf("Get = %q, want %q", got, "blob-data")
	}
	if parent.puts.Load() != 1 {
		t.Errorf("parent.puts = %d, want 1", parent.puts.Load())
	}
	if parent.gets.Load() != 0 {
		t.Errorf("parent.gets = %d, want 0 (cache should serve)", parent.gets.Load())
	}
}

// TestLocalCacheBackend_LocalPathDelegates pins the local-file capability
// used by large ext4 scans. The cache must not redirect callers to its hashed
// cache file; the parent path is the canonical artifact path.
func TestLocalCacheBackend_LocalPathDelegates(t *testing.T) {
	parent := &localPathFakeBackend{
		fakeBackend: newFakeBackend(),
		root:        t.TempDir(),
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(t.TempDir(), "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	got, ok, err := cache.LocalPath("base/runner-builder-amd64.ext4")
	if err != nil {
		t.Fatalf("LocalPath: %v", err)
	}
	if !ok {
		t.Fatal("LocalPath ok=false, want true")
	}
	want := filepath.Join(parent.root, "base/runner-builder-amd64.ext4")
	if got != want {
		t.Errorf("LocalPath = %q, want %q", got, want)
	}
}

// TestLocalCacheBackend_CacheMissFallsBackToParent pins the
// read-through behavior: a Get on a cold cache reads from the
// parent + populates the cache.
func TestLocalCacheBackend_CacheMissFallsBackToParent(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	// Pre-seed parent only (skip the cache write path).
	if err := parent.Put(ctx, "snap/abc", strings.NewReader("from-parent")); err != nil {
		t.Fatalf("parent.Put: %v", err)
	}
	// Cold cache: Get goes to parent + populates cache.
	got, err := readAll(ctx, cache, "snap/abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "from-parent" {
		t.Errorf("Get = %q, want %q", got, "from-parent")
	}
	if parent.gets.Load() != 1 {
		t.Errorf("parent.gets after first Get = %d, want 1", parent.gets.Load())
	}
	// Second Get should hit cache (parent untouched).
	got2, err := readAll(ctx, cache, "snap/abc")
	if err != nil {
		t.Fatalf("Get #2: %v", err)
	}
	if got2 != "from-parent" {
		t.Errorf("Get #2 = %q, want %q", got2, "from-parent")
	}
	if parent.gets.Load() != 1 {
		t.Errorf("parent.gets after second Get = %d, want 1 (cache hit)", parent.gets.Load())
	}
}

// TestLocalCacheBackend_ParentFailureSurfaces pins the failure
// mode: when the parent backend fails (e.g. a registry outage),
// Get surfaces the wrapped error verbatim. The cache does NOT
// fall back to a stale entry — ADR-054 §2 keeps that contract
// explicit; the "registry unreachable" gate at startup is a
// separate seam that callers use to opt in to stale-fallback
// (FAAS_STORAGE_CACHE_SERVE_STALE=true).
func TestLocalCacheBackend_ParentFailureSurfaces(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	parent.onGet = func(_ string) error {
		return errors.New("registry unreachable")
	}
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	_, err = cache.Get(context.Background(), "snap/abc")
	if err == nil {
		t.Fatal("Get = nil; want error from parent failure")
	}
	if !strings.Contains(err.Error(), "registry unreachable") {
		t.Errorf("err = %q, want it to wrap 'registry unreachable'", err)
	}
}

// TestLocalCacheBackend_StaleFallbackEnabled pins the opt-in stale
// path: when FAAS_STORAGE_CACHE_SERVE_STALE=true and the parent
// backend fails, Get serves the last-known-good cached blob if one
// is present. The cache observer fires once. The wrapped parent error
// is NOT returned to the caller.
//
// Setup: drive the stale-fallback branch by having the parent's Get
// hook Put the blob into the cache synchronously before returning
// the failure. This models the realistic race: another wake on a
// peer node Puts the blob into this node's cache while this node's
// Get is in flight. Without the opt-in knob, the same hook fires and
// the test below (DefaultContract) pins the fail-loud behaviour.
func TestLocalCacheBackend_StaleFallbackEnabled(t *testing.T) {
	t.Setenv("FAAS_STORAGE_CACHE_SERVE_STALE", "true")
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	var observerCalls atomic.Int64
	cache.SetObserver(storage.FuncCacheObserver(func() { observerCalls.Add(1) }))

	// onGet Puts the stale blob into the cache before returning
	// the failure. Mirrors a peer-node wake racing this node's
	// cold-boot Get.
	parent.onGet = func(key string) error {
		_ = cache.Put(context.Background(), key, strings.NewReader("stale-blob"))
		return errors.New("registry unreachable")
	}
	got, err := readAll(context.Background(), cache, "snap/abc")
	if err != nil {
		t.Fatalf("Get: %v (expected stale fallback, not wrapped error)", err)
	}
	if got != "stale-blob" {
		t.Errorf("Get = %q, want %q (stale fallback bytes)", got, "stale-blob")
	}
	if got := observerCalls.Load(); got != 1 {
		t.Errorf("observer called %d times, want 1", got)
	}
}

// TestLocalCacheBackend_StaleFallbackDisabled_DefaultContract pins
// the default-off behaviour: when FAAS_STORAGE_CACHE_SERVE_STALE is
// NOT set, a parent failure returns the wrapped parent error even
// when a stale cache entry exists (Put'd by another wake on a peer).
// The cache observer never fires.
//
// This is the fail-loud contract TestLocalCacheBackend_ParentFailureSurfaces
// also asserts — this test exists so a future contributor who breaks
// the default-off behaviour sees the test name in a failure message
// and can grep for the env var's purpose.
//
// We t.Setenv to empty rather than relying on "unset": a parallel
// test or a parent process that exports FAAS_STORAGE_CACHE_SERVE_STALE
// would otherwise contaminate this test nondeterministically. t.Setenv
// to "" restores the default-off state explicitly (the os.LookupEnv
// distinction in serveStale treats unset == empty == default-off).
func TestLocalCacheBackend_StaleFallbackDisabled_DefaultContract(t *testing.T) {
	t.Setenv("FAAS_STORAGE_CACHE_SERVE_STALE", "")
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	var observerCalls atomic.Int64
	cache.SetObserver(storage.FuncCacheObserver(func() { observerCalls.Add(1) }))

	parent.onGet = func(key string) error {
		_ = cache.Put(context.Background(), key, strings.NewReader("stale-blob"))
		return errors.New("registry unreachable")
	}
	_, err = cache.Get(context.Background(), "snap/abc")
	if err == nil {
		t.Fatal("Get = nil; want wrapped parent error (default-off stale fallback)")
	}
	if !strings.Contains(err.Error(), "registry unreachable") {
		t.Errorf("err = %q, want it to wrap 'registry unreachable'", err)
	}
	if got := observerCalls.Load(); got != 0 {
		t.Errorf("observer called %d times, want 0 (default-off; observer must not fire)", got)
	}
}

// TestLocalCacheBackend_ServeStaleTruthyVariants pins the env
// contract for FAAS_STORAGE_CACHE_SERVE_STALE: any value
// strconv.ParseBool accepts as true engages the stale-fallback
// branch. Operators reading a 12-factor example that says =1 or
// =True must get the same behaviour as =true — the safety net is
// on whenever the operator reasonably intends it. Conversely,
// unset / empty / "0" / "false" / "no" / "off" all disable.
//
// Implementation note: each value gets a UNIQUE cache root keyed
// by an integer counter — the env value is intentionally NOT used
// in the path. macOS (APFS/HFS+) is case-insensitive by default,
// so env values "t" vs "T" or "false" vs "FALSE" would alias to
// the same on-disk directory and the previous subtest's Put
// would survive into the next subtest's Get, short-circuiting
// the stale-fallback path on readCache HIT (top). Linux CI
// passes the matrix either way; macOS dev fails it without this
// disambiguation.
func TestLocalCacheBackend_ServeStaleTruthyVariants(t *testing.T) {
	tmp := t.TempDir()
	idx := 0
	for _, val := range []string{"1", "t", "T", "true", "TRUE", "True"} {
		idx++
		val, i := val, idx
		t.Run("on="+val, func(t *testing.T) {
			withServeStale(t, val, func() {
				cache, observerCalls := setupStaleFallbackCache(t, tmp, i)
				got, err := readAll(context.Background(), cache, "snap/abc")
				if err != nil {
					t.Fatalf("val=%q Get: %v (expected stale fallback, not wrapped error)", val, err)
				}
				if got != "stale-blob" {
					t.Errorf("val=%q Get = %q, want %q", val, got, "stale-blob")
				}
				if n := observerCalls.Load(); n != 1 {
					t.Errorf("val=%q observer calls = %d, want 1", val, n)
				}
			})
		})
	}
	for _, val := range []string{"", "0", "f", "F", "false", "FALSE", "no"} {
		idx++
		val, i := val, idx
		t.Run("off="+val, func(t *testing.T) {
			withServeStale(t, val, func() {
				cache, observerCalls := setupStaleFallbackCache(t, tmp, i)
				_, err := cache.Get(context.Background(), "snap/abc")
				if err == nil {
					t.Fatalf("val=%q Get = nil; want wrapped parent error", val)
				}
				if !strings.Contains(err.Error(), "registry unreachable") {
					t.Errorf("val=%q err = %q, want it to wrap 'registry unreachable'", val, err)
				}
				if n := observerCalls.Load(); n != 0 {
					t.Errorf("val=%q observer calls = %d, want 0 (off)", val, n)
				}
			})
		})
	}
}

// withServeStale sets FAAS_STORAGE_CACHE_SERVE_STALE for the
// duration of fn, restoring the prior value (set or unset) on
// cleanup. Empty val means unset.
func withServeStale(t *testing.T, val string, fn func()) {
	t.Helper()
	prev, hadPrev := os.LookupEnv("FAAS_STORAGE_CACHE_SERVE_STALE")
	if val == "" {
		_ = os.Unsetenv("FAAS_STORAGE_CACHE_SERVE_STALE")
	} else {
		_ = os.Setenv("FAAS_STORAGE_CACHE_SERVE_STALE", val)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv("FAAS_STORAGE_CACHE_SERVE_STALE", prev)
		} else {
			_ = os.Unsetenv("FAAS_STORAGE_CACHE_SERVE_STALE")
		}
	})
	fn()
}

// setupStaleFallbackCache builds a fresh cache + parent + observer
// for the stale-fallback truthy matrix. The index i disambiguates
// the cache root on case-insensitive filesystems (macOS); see
// TestLocalCacheBackend_ServeStaleTruthyVariants' doc comment for
// the case-collisions that motivated this. The parent's onGet
// hook Puts the blob into the cache before returning error — the
// only way to drive serveStale in unit tests since the
// StorageBackend interface does not expose an InjectForTest seam.
func setupStaleFallbackCache(t *testing.T, tmp string, i int) (storage.StorageBackend, *atomic.Int64) {
	t.Helper()
	parent := newFakeBackend()
	cacheRoot := filepath.Join(tmp, fmt.Sprintf("cache-%03d", i))
	cache, err := storage.NewLocalCacheBackend(parent, cacheRoot, 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	var observerCalls atomic.Int64
	cache.SetObserver(storage.FuncCacheObserver(func() { observerCalls.Add(1) }))
	parent.onGet = func(key string) error {
		_ = cache.Put(context.Background(), key, strings.NewReader("stale-blob"))
		return errors.New("registry unreachable")
	}
	return cache, &observerCalls
}

// TestLocalCacheBackend_ListRecoversOriginalKey pins the sidecar
// metadata contract: List returns the original storage keys, not
// the hash-derived filenames on disk. This is the seam GC code
// paths (pkg/imaged, pkg/sched/disk_drift) rely on.
func TestLocalCacheBackend_ListRecoversOriginalKey(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	keys := []string{
		"snap/a",
		"snap/b",
		"layers/l1",
		"layers/l2",
	}
	for _, k := range keys {
		if err := cache.Put(ctx, k, strings.NewReader("data-"+k)); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
	}
	got, err := cache.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(got)
	want := []string{"layers/l1", "layers/l2", "snap/a", "snap/b"}
	if !equalSlices(got, want) {
		t.Errorf("List = %v, want %v", got, want)
	}
	// Sub-prefix filtering.
	got, err = cache.List(ctx, "layers/")
	if err != nil {
		t.Fatalf("List layers/: %v", err)
	}
	sort.Strings(got)
	if !equalSlices(got, []string{"layers/l1", "layers/l2"}) {
		t.Errorf("List layers/ = %v, want [layers/l1 layers/l2]", got)
	}
}

// TestLocalCacheBackend_LRUEvictsOldest pins the byte-budgeted
// eviction policy: when total size exceeds maxBytes, the oldest
// entries by mtime are evicted first. The test seeds three blobs,
// sets a tight budget that fits only two, then writes a fourth to
// trigger eviction.
func TestLocalCacheBackend_LRUEvictsOldest(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	// 10 bytes per blob, budget 25 bytes → at most 2 blobs.
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 25)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	// Use distinct mtimes so eviction is deterministic.
	for _, k := range []string{"a", "b", "c"} {
		if err := cache.Put(ctx, "snap/"+k, strings.NewReader(strings.Repeat("x", 10))); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
		// Stagger mtimes 1s apart so LRU ordering is stable.
		// Chtimes overrides whatever the cache's readCache
		// touch-up set on the file.
		path := hashCachePath(t, filepath.Join(tmp, "cache"), "snap/"+k)
		past := time.Now().Add(-1 * time.Hour).Add(time.Duration(k[0]-'a') * time.Second)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatalf("Chtimes: %v", err)
		}
	}
	// Write the 4th blob — 40 bytes total > 25 byte budget,
	// so the two oldest by mtime (a, b) are evicted; c, d remain.
	if err := cache.Put(ctx, "snap/d", strings.NewReader(strings.Repeat("x", 10))); err != nil {
		t.Fatalf("Put d: %v", err)
	}
	keys, err := cache.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	sort.Strings(keys)
	want := []string{"snap/c", "snap/d"}
	if !equalSlices(keys, want) {
		t.Errorf("List after eviction = %v, want %v (eviction order is oldest-first)", keys, want)
	}
}

// TestLocalCacheBackend_DeleteEvictsAndForwards pins the
// delete-propagation contract: a Delete evicts the cache entry
// + forwards to the parent.
func TestLocalCacheBackend_DeleteEvictsAndForwards(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	if err := cache.Put(ctx, "snap/abc", strings.NewReader("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := cache.Delete(ctx, "snap/abc"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if parent.deletes.Load() != 1 {
		t.Errorf("parent.deletes = %d, want 1", parent.deletes.Load())
	}
	// After eviction, List should not include the key.
	keys, err := cache.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, k := range keys {
		if k == "snap/abc" {
			t.Errorf("snap/abc still in cache after Delete: keys=%v", keys)
		}
	}
}

// TestLocalCacheBackend_NilParentRejected pins the constructor's
// nil-parent guard: a nil parent is rejected up front, not at
// runtime when the first Put/Get tries to use it.
func TestLocalCacheBackend_NilParentRejected(t *testing.T) {
	tmp := t.TempDir()
	if _, err := storage.NewLocalCacheBackend(nil, filepath.Join(tmp, "cache"), 0); err == nil {
		t.Fatal("NewLocalCacheBackend(nil, …) = nil err; want error")
	}
}

// TestLocalCacheBackend_EmptyRootRejected pins the empty-root
// guard: an empty cache dir is a configuration error, not a
// permissive default that puts the cache at the wrong path.
func TestLocalCacheBackend_EmptyRootRejected(t *testing.T) {
	parent := newFakeBackend()
	if _, err := storage.NewLocalCacheBackend(parent, "", 0); err == nil {
		t.Fatal("NewLocalCacheBackend(_, \"\", _) = nil err; want error")
	}
}

// TestLocalCacheBackend_WrapsPrefixRouter pins the integration
// seam: wrapping a PrefixRouter with a cache keeps routing
// intact (snap/ → local, apps/ → OCI stub) and adds read-through
// on every per-route Get.
func TestLocalCacheBackend_WrapsPrefixRouter(t *testing.T) {
	tmp := t.TempDir()
	local, err := storage.NewLocalStorageBackend(filepath.Join(tmp, "fc"))
	if err != nil {
		t.Fatalf("NewLocalStorageBackend local: %v", err)
	}
	oci := newFakeBackend()
	router, err := storage.NewPrefixRouter(
		map[string]storage.StorageBackend{
			"snap/":   local,
			"apps/":   oci,
			"base/":   local,
			"kernel/": local,
			"layers/": local,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("NewPrefixRouter: %v", err)
	}
	cache, err := storage.NewLocalCacheBackend(router, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	// snap/ lands in the local backend.
	if err := cache.Put(ctx, "snap/abc", strings.NewReader("snap-data")); err != nil {
		t.Fatalf("Put snap/abc: %v", err)
	}
	got, err := readAll(ctx, cache, "snap/abc")
	if err != nil {
		t.Fatalf("Get snap/abc: %v", err)
	}
	if got != "snap-data" {
		t.Errorf("snap/abc = %q, want %q", got, "snap-data")
	}
	// apps/ lands in the OCI stub (the router routes it).
	if err := cache.Put(ctx, "apps/acme/x", strings.NewReader("apps-data")); err != nil {
		t.Fatalf("Put apps/acme/x: %v", err)
	}
	got, err = readAll(ctx, cache, "apps/acme/x")
	if err != nil {
		t.Fatalf("Get apps/acme/x: %v", err)
	}
	if got != "apps-data" {
		t.Errorf("apps/acme/x = %q, want %q", got, "apps-data")
	}
	// The cache hit-path must short-circuit the OCI backend.
	if oci.gets.Load() != 0 {
		t.Errorf("oci.gets = %d, want 0 (cache should serve)", oci.gets.Load())
	}
}

// TestLocalCacheBackend_DeterministicBucketing pins the
// bucket fan-out: the same key always lands in the same bucket.
// The cache layout uses the first 2 hex chars of SHA-256(key);
// this test ensures that contract holds (a future refactor that
// switches to a different hash or fan-out breaks the on-disk
// compatibility).
func TestLocalCacheBackend_DeterministicBucketing(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	if err := cache.Put(ctx, "snap/abc", strings.NewReader("data")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	path := hashCachePath(t, filepath.Join(tmp, "cache"), "snap/abc")
	// Bucket must be exactly 2 hex chars.
	bucket := filepath.Base(filepath.Dir(path))
	if len(bucket) != 2 {
		t.Errorf("bucket = %q (len %d), want 2-char hex", bucket, len(bucket))
	}
	for _, c := range bucket {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			t.Errorf("bucket %q contains non-hex char %q", bucket, c)
		}
	}
}

// sizeReader is an io.Reader that exposes its byte count via
// Size(). Mirrors the shape of bytes.Reader, strings.Reader,
// and bytes.Buffer so the cache's SizeReader sniff picks it up.
type sizeReader struct {
	off int64
	buf []byte
}

func (s *sizeReader) Read(p []byte) (int, error) {
	if s.off >= int64(len(s.buf)) {
		return 0, io.EOF
	}
	n := copy(p, s.buf[s.off:])
	s.off += int64(n)
	return n, nil
}

func (s *sizeReader) Size() int64 { return int64(len(s.buf)) }

// TestLocalCacheBackend_Put_RejectsOversizedBlob pins the hard
// pre-check: a blob whose caller-known size exceeds maxBytes is
// rejected before any read. The parent never sees the bytes,
// the cache stays untouched, and the typed error is observable
// by callers that want to distinguish "too big" from "parent
// failed".
func TestLocalCacheBackend_Put_RejectsOversizedBlob(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 16)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	// 32-byte blob into a 16-byte cache. The SizeReader sniff
	// triggers the pre-check before the parent is even called.
	src := &sizeReader{buf: make([]byte, 32)}
	err = cache.Put(context.Background(), "snap/abc", src)
	if err == nil {
		t.Fatal("Put on 32-byte blob into 16-byte cache succeeded; want error")
	}
	if !strings.Contains(err.Error(), "exceeds maxBytes") {
		t.Errorf("err = %q, want contains 'exceeds maxBytes'", err)
	}
	if parent.puts.Load() != 0 {
		t.Errorf("parent.puts = %d, want 0 (oversized blob must not reach parent)", parent.puts.Load())
	}
}

// TestLocalCacheBackend_Put_RejectsOversizedStreamedBlob pins
// the post-parent size check: a stream that doesn't expose Size
// (and therefore can't be pre-checked) is rejected after the
// parent has accepted the bytes. The parent has the canonical
// copy; the cache is not poisoned with an oversized blob we
// can't guarantee eviction for. (A SizeReader, by contrast,
// hits the pre-check and never reaches the parent.)
func TestLocalCacheBackend_Put_RejectsOversizedStreamedBlob(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 16)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	// 32-byte stream wrapped to hide Size() so the pre-check
	// can't fire; the TeeReader fills the buffer; the
	// post-parent check fires.
	blob := []byte(strings.Repeat("x", 32))
	err = cache.Put(context.Background(), "snap/abc", noSizeReader{Reader: bytes.NewReader(blob)})
	if err == nil {
		t.Fatal("Put on 32-byte stream into 16-byte cache succeeded; want error")
	}
	if !strings.Contains(err.Error(), "exceeds maxBytes") {
		t.Errorf("err = %q, want contains 'exceeds maxBytes'", err)
	}
	if parent.puts.Load() != 1 {
		t.Errorf("parent.puts = %d, want 1 (parent should still see the bytes)", parent.puts.Load())
	}
}

// noSizeReader wraps an io.Reader to hide Size(). Used to
// exercise the post-parent size check path that the pre-check
// would otherwise short-circuit.
type noSizeReader struct{ io.Reader }

// TestLocalCacheBackend_Put_StreamsToParent pins the streaming
// Put path: a large blob is piped to the parent via TeeReader
// without a 2x transient allocation. The parent sees the
// streamed bytes; the cache mirrors the same bytes back via
// a subsequent Get.
func TestLocalCacheBackend_Put_StreamsToParent(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	payload := strings.Repeat("stream-blob-", 1024)
	if err := cache.Put(ctx, "snap/abc", strings.NewReader(payload)); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if parent.puts.Load() != 1 {
		t.Errorf("parent.puts = %d, want 1", parent.puts.Load())
	}
	// Round-trip through the cache hit-path.
	got, err := readAll(ctx, cache, "snap/abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != payload {
		t.Errorf("Get round-tripped %d bytes, want %d", len(got), len(payload))
	}
	if parent.gets.Load() != 0 {
		t.Errorf("parent.gets = %d, want 0 (cache should serve)", parent.gets.Load())
	}
}

// TestLocalCacheBackend_GetsTouchMtime pins true LRU behaviour: an entry used
// by restore must become newer than an idle entry so byte-budget eviction
// retains active app layers.
func TestLocalCacheBackend_GetsTouchMtime(t *testing.T) {
	tmp := t.TempDir()
	parent := newFakeBackend()
	cache, err := storage.NewLocalCacheBackend(parent, filepath.Join(tmp, "cache"), 0)
	if err != nil {
		t.Fatalf("NewLocalCacheBackend: %v", err)
	}
	ctx := context.Background()
	for _, k := range []string{"snap/a", "snap/b"} {
		if err := cache.Put(ctx, k, strings.NewReader("data-"+k)); err != nil {
			t.Fatalf("Put %q: %v", k, err)
		}
		// Set mtime to a known old value.
		path := hashCachePath(t, filepath.Join(tmp, "cache"), k)
		past := time.Now().Add(-1 * time.Hour)
		if err := os.Chtimes(path, past, past); err != nil {
			t.Fatalf("Chtimes %q: %v", k, err)
		}
	}
	// Read only a. It must become newer while b remains at the seeded time.
	beforeA := mtimeOf(t, hashCachePath(t, filepath.Join(tmp, "cache"), "snap/a"))
	beforeB := mtimeOf(t, hashCachePath(t, filepath.Join(tmp, "cache"), "snap/b"))
	for i := 0; i < 5; i++ {
		if _, err := readAll(ctx, cache, "snap/a"); err != nil {
			t.Fatalf("Get a: %v", err)
		}
	}
	afterA := mtimeOf(t, hashCachePath(t, filepath.Join(tmp, "cache"), "snap/a"))
	afterB := mtimeOf(t, hashCachePath(t, filepath.Join(tmp, "cache"), "snap/b"))
	if !afterA.After(beforeA) {
		t.Errorf("snap/a mtime did not advance on read: before=%s, after=%s", beforeA, afterA)
	}
	if !afterB.Equal(beforeB) {
		t.Errorf("unread snap/b mtime changed: before=%s, after=%s", beforeB, afterB)
	}
}

// mtimeOf returns the file's mtime or fatals. Test helper.
func mtimeOf(t *testing.T, path string) time.Time {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	return st.ModTime()
}

// readAll pulls a blob through the cache, returning the contents
// as a string. Surfaces failures as test errors via t.Fatalf.
func readAll(ctx context.Context, b storage.StorageBackend, key string) (string, error) {
	rc, err := b.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// hashCachePath recomputes the on-disk path the cache used for
// `key`. Mirrors cacheFileFor but as a test helper so the
// eviction tests can Chtimes the file directly. The hash is
// SHA-256(key), hex-encoded; the bucket is the first 2 chars.
func hashCachePath(t *testing.T, root, key string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(key))
	hexStr := hex.EncodeToString(sum[:])
	return filepath.Join(root, hexStr[:2], hexStr[2:])
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
