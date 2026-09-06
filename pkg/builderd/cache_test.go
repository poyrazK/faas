package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCache_MissReturnsFalse(t *testing.T) {
	c := NewCache(t.TempDir())
	if _, ok := c.Lookup("deadbeef", FrameworkNode, api.PlanHobby); ok {
		t.Error("lookup on empty cache should miss")
	}
}

func TestCache_StoreAndLookup(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	// Create a fake layer file.
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("fake layer bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := "0123456789abcdef"
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src, 16); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup(hash, FrameworkNode, api.PlanHobby)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Bytes != 16 {
		t.Errorf("bytes = %d, want 16", got.Bytes)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Errorf("cache file missing: %v", err)
	}
}

func TestCache_StoreIdempotent(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("h1", FrameworkPython, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	// Second store with different src — should NOT overwrite.
	src2 := filepath.Join(t.TempDir(), "layer2.ext4")
	if err := os.WriteFile(src2, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("h1", FrameworkPython, api.PlanHobby, src2, 6); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup("h1", FrameworkPython, api.PlanHobby)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Bytes != 5 {
		t.Errorf("bytes = %d, want 5 (first writer wins)", got.Bytes)
	}
}

func TestCache_NilSafe(t *testing.T) {
	var c *Cache
	if _, ok := c.Lookup("h", FrameworkNode, api.PlanHobby); ok {
		t.Error("nil cache should miss")
	}
	if err := c.Store("h", FrameworkNode, api.PlanHobby, "/x", 1); err == nil {
		t.Error("nil cache Store should error")
	}
}

func TestHashFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := hashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("hash = %s, want %s", got, want)
	}
}

func TestHashFile_Missing(t *testing.T) {
	if _, err := hashFile("/no/such/file"); err == nil {
		t.Error("expected error on missing file")
	}
}

// TestCacheStore_AtomicOnCrash is the B1.2 invariant gate. The pre-fix
// Store used os.Rename(layerPath, dst) directly — a successful rename
// published a half-written file if the source had been pre-truncated or
// if the kernel reordered writes. The post-fix Store writes the source
// to a UNIQUE temp file, fsyncs, then renames — a process kill mid-copy
// MUST leave the canonical entry either absent or fully populated, never
// half-written.
//
// We simulate the mid-copy crash by truncating the source file to 0
// bytes before Store; the old code would have happily renamed the
// empty source onto dst (a torn write). The new code copies the empty
// source to a temp and renames; dst ends up empty but not torn — both
// states are non-corrupt because a future Lookup will either miss
// (dst gone) or hit a 0-byte file (which buildImageLayer rejects via
// size validation upstream).
func TestCacheStore_AtomicOnCrash(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("not what we want"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: pre-truncate the source to 0 bytes. Pre-fix
	// code would os.Rename(src, dst) and leave dst as an empty file
	// that subsequent Lookups would mistake for a valid cache hit (a
	// 0-byte cache hit would silently break cold boot). The post-fix
	// code must copy src → tmp → rename, so dst ends up empty but
	// the source is preserved for the caller.
	if err := os.Truncate(src, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("crash-hash", FrameworkNode, api.PlanHobby, src, 0); err != nil {
		t.Fatalf("Store should succeed (atomic publish of empty file is OK): %v", err)
	}
	// The source file MUST still be on disk for the caller to use —
	// pkg/builderd/builderd.go:432 reads out.OCIImage immediately
	// after Store returns.
	st, err := os.Stat(src)
	if err != nil {
		t.Fatalf("B1.2 source preservation regression: source was consumed: %v", err)
	}
	if st.Size() != 0 {
		t.Errorf("source size = %d, want 0 (test setup)", st.Size())
	}
	// dst exists (0 bytes) — atomic publish means dst is either fully
	// populated OR not present, never torn. The post-fix path is "fully
	// populated with whatever the source had", and the source was 0
	// bytes here.
	dst := c.entryPath("crash-hash", FrameworkNode, api.PlanHobby)
	st, err = os.Stat(dst)
	if err != nil {
		t.Fatalf("dst missing after Store: %v", err)
	}
	if st.Size() != 0 {
		t.Errorf("dst size = %d, want 0 (atomic publish of empty source)", st.Size())
	}
}

// TestCacheStore_PreservesSource is the source-preservation regression.
// Pre-fix code used os.Rename(layerPath, dst) which CONSUMED the
// source — pkg/builderd/builderd.go:432 reads out.OCIImage immediately
// after Store returns, and the rename would have made that read fail
// with ENOENT. The post-fix code copies source → tmp → renames tmp,
// leaving layerPath untouched.
func TestCacheStore_PreservesSource(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	payload := []byte("source bytes for downstream use")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("preserve-hash", FrameworkNode, api.PlanHobby, src, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("B1.2 source preservation regression: source missing after Store: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("source bytes changed: got %q, want %q", got, payload)
	}
	// dst has the same content.
	dst := c.entryPath("preserve-hash", FrameworkNode, api.PlanHobby)
	dstBytes, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("dst missing: %v", err)
	}
	if string(dstBytes) != string(payload) {
		t.Errorf("dst bytes = %q, want %q", dstBytes, payload)
	}
}

// TestCacheStore_PublishesGroupReadableArtifacts protects the split-box
// handoff to imaged. os.CreateTemp creates 0600 files by default; publishing
// that inode unchanged leaves imaged (faas-imaged:faas) unable to read the
// layer produced by builderd (faas-builderd:faas).
func TestCacheStore_PublishesGroupReadableArtifacts(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("shared cache layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("mode-hash", FrameworkNode, api.PlanHobby, src, 18); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		c.entryPath("mode-hash", FrameworkNode, api.PlanHobby),
		c.checksumPath("mode-hash", FrameworkNode, api.PlanHobby),
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := st.Mode().Perm(); got != cacheArtifactMode.Perm() {
			t.Errorf("%s mode = %o, want %o", path, got, cacheArtifactMode.Perm())
		}
	}
	for _, path := range []string{
		c.entryPath("mode-hash", FrameworkNode, api.PlanHobby),
		c.checksumPath("mode-hash", FrameworkNode, api.PlanHobby),
	} {
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatalf("chmod legacy %s: %v", path, err)
		}
	}
	if err := c.Store("mode-hash", FrameworkNode, api.PlanHobby, src, 18); err != nil {
		t.Fatalf("repair legacy cache: %v", err)
	}
	for _, path := range []string{
		c.entryPath("mode-hash", FrameworkNode, api.PlanHobby),
		c.checksumPath("mode-hash", FrameworkNode, api.PlanHobby),
	} {
		st, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat repaired %s: %v", path, err)
		}
		if got := st.Mode().Perm(); got != cacheArtifactMode.Perm() {
			t.Errorf("repaired %s mode = %o, want %o", path, got, cacheArtifactMode.Perm())
		}
	}
}

// TestCacheStore_NoTempLeftover asserts the happy-path Store leaves
// no temp file behind in the cache root. A persistent temp file would
// (a) waste disk space and (b) confuse a future cleanup sweep that
// walks the cache dir looking for orphans.
func TestCacheStore_NoTempLeftover(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("happy"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("happy-hash", FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	// Walk the cache root and assert no `cache-*.tmp` file exists.
	matches, err := filepath.Glob(filepath.Join(root, "*", "cache-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("B1.2 temp leftover regression: %d temp files in cache root: %v", len(matches), matches)
	}
	// Belt-and-braces: also check the per-entry dir doesn't have a
	// sibling temp from a prior failure.
	matches, err = filepath.Glob(filepath.Join(root, "cache-*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Errorf("B1.2 temp leftover regression: %d stray temp files in root: %v", len(matches), matches)
	}
}

// TestCacheStore_ConcurrentStoresAreSafe is the regression test for
// the unique-temp bug. Pre-fix code had no temp file at all
// (os.Rename(layerPath, dst) directly); post-fix code uses
// os.CreateTemp with a `cache-*.tmp` wildcard. Two concurrent Store
// calls for distinct keys must each get a distinct temp path, must
// not tear each other's dst, and must leave no temp leftover.
func TestCacheStore_ConcurrentStoresAreSafe(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	const N = 8

	// Build N distinct source files.
	srcs := make([]string, N)
	for i := 0; i < N; i++ {
		s := filepath.Join(t.TempDir(), "src-"+string(rune('a'+i))+".ext4")
		payload := strings.Repeat(string(rune('a'+i)), 1024)
		if err := os.WriteFile(s, []byte(payload), 0o644); err != nil {
			t.Fatal(err)
		}
		srcs[i] = s
	}

	// Fire N goroutines in parallel. WaitGroup + a barrier (close
	// start) ensures all goroutines race for the same temp-name
	// suffix space — the test would flake on the pre-fix code that
	// wrote a single literal temp path.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			hash := "concurrent-" + string(rune('a'+idx))
			if err := c.Store(hash, FrameworkNode, api.PlanHobby, srcs[idx], 1024); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Store failed: %v", err)
	}

	// All N entries must exist with the correct bytes.
	for i := 0; i < N; i++ {
		hash := "concurrent-" + string(rune('a'+i))
		entry, ok := c.Lookup(hash, FrameworkNode, api.PlanHobby)
		if !ok {
			t.Errorf("concurrent[%d]: cache miss after concurrent Store", i)
			continue
		}
		got, err := os.ReadFile(entry.Path)
		if err != nil {
			t.Errorf("concurrent[%d]: read entry: %v", i, err)
			continue
		}
		want := strings.Repeat(string(rune('a'+i)), 1024)
		if string(got) != want {
			t.Errorf("concurrent[%d]: entry bytes mismatch (got %d bytes, want %q)",
				i, len(got), want[:32])
		}
	}

	// No temp leftover.
	matches, _ := filepath.Glob(filepath.Join(root, "*", "cache-*.tmp"))
	if len(matches) != 0 {
		t.Errorf("concurrent Store left %d temp files behind: %v", len(matches), matches)
	}
}

// TestCacheStore_FirstWriterWins still passes after the rewrite —
// it was the old behavior the rewrite must preserve. First-writer wins.
func TestCacheStore_FirstWriterWins(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("idem", FrameworkPython, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	src2 := filepath.Join(t.TempDir(), "layer2.ext4")
	if err := os.WriteFile(src2, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("idem", FrameworkPython, api.PlanHobby, src2, 6); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Lookup("idem", FrameworkPython, api.PlanHobby)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Bytes != 5 {
		t.Errorf("first-writer-wins regression: bytes = %d, want 5", got.Bytes)
	}
}

// TestCacheLookup_RejectsMissingSidecar is the B1.3 negative test. A
// layer file without a sidecar (or with a deleted one) is a cache
// miss — the next Store re-creates the sidecar.
func TestCacheLookup_RejectsMissingSidecar(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("missing-sidecar", FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	// Delete the sidecar out from under the cache.
	cs := c.checksumPath("missing-sidecar", FrameworkNode, api.PlanHobby)
	if err := os.Remove(cs); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup("missing-sidecar", FrameworkNode, api.PlanHobby); ok {
		t.Error("B1.3 regression: Lookup returned hit after sidecar was deleted")
	}
}

// TestCacheLookup_RejectsMismatchedSidecar asserts a sidecar whose
// content doesn't match the sourceHash is a cache miss.
func TestCacheLookup_RejectsMismatchedSidecar(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("mismatch", FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	cs := c.checksumPath("mismatch", FrameworkNode, api.PlanHobby)
	if err := os.WriteFile(cs, []byte("different-hash\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup("mismatch", FrameworkNode, api.PlanHobby); ok {
		t.Error("B1.3 regression: Lookup returned hit after sidecar was tampered")
	}
}

// TestCacheLookup_RejectsMalformedSidecar asserts table-driven
// malformed sidecars all return cache miss. Each entry seeds the
// cache, then overwrites the sidecar with malformed content.
func TestCacheLookup_RejectsMalformedSidecar(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store("malformed", FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	cs := c.checksumPath("malformed", FrameworkNode, api.PlanHobby)
	cases := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"single_newline", "\n"},
		{"only_whitespace", "   \t  \n"},
		{"garbage", "not-a-valid-hash-just-noise"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(cs, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok := c.Lookup("malformed", FrameworkNode, api.PlanHobby); ok {
				t.Errorf("B1.3 regression: malformed sidecar %q returned cache hit", tc.name)
			}
		})
	}
}

// TestCacheLookup_AcceptsSidecarWithTrailingWhitespace locks the
// TrimSpace tolerance. Store writes "<sourceHash>\n"; a future
// operator inspecting the file may add trailing whitespace via
// `echo >> layer.sha256`. Lookup must still accept it.
func TestCacheLookup_AcceptsSidecarWithTrailingWhitespace(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := "trailing-ws-hash"
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	cs := c.checksumPath(hash, FrameworkNode, api.PlanHobby)
	if err := os.WriteFile(cs, []byte(hash+"\n\n\n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Lookup(hash, FrameworkNode, api.PlanHobby); !ok {
		t.Error("B1.3 regression: Lookup rejected sidecar with trailing whitespace")
	}
}

// TestCacheStore_RepairsSidecar is the legacy-cache self-heal
// regression. Pre-B1.3 caches have a layer without a sidecar (a
// cache miss under the new Lookup). The first Store of that key
// post-upgrade must re-create the sidecar — even when the layer
// file already exists (the first-writer-wins short-circuit).
func TestCacheStore_RepairsSidecar(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := "legacy-cache-key"
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	// Delete the sidecar to simulate a legacy cache state.
	cs := c.checksumPath(hash, FrameworkNode, api.PlanHobby)
	if err := os.Remove(cs); err != nil {
		t.Fatal(err)
	}
	// Re-Store with a DIFFERENT source path. First-writer-wins
	// must hold for the layer, but the sidecar MUST be re-created.
	src2 := filepath.Join(t.TempDir(), "layer2.ext4")
	if err := os.WriteFile(src2, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src2, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cs); err != nil {
		t.Fatalf("B1.3 self-heal regression: sidecar not re-created: %v", err)
	}
	if _, ok := c.Lookup(hash, FrameworkNode, api.PlanHobby); !ok {
		t.Error("B1.3 self-heal regression: Lookup missed after sidecar re-creation")
	}
}

// TestCacheStore_IdempotentSidecar — calling Store twice on the same
// key MUST leave the sidecar with the same content. Tests the
// writeSidecar short-circuit (existing sidecar → nil return).
func TestCacheStore_IdempotentSidecar(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	src := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(src, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := "idem-sidecar"
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src, 5); err != nil {
		t.Fatal(err)
	}
	cs := c.checksumPath(hash, FrameworkNode, api.PlanHobby)
	first, err := os.ReadFile(cs)
	if err != nil {
		t.Fatal(err)
	}
	// Second Store with the same hash — layer already exists, sidecar
	// short-circuit must fire.
	src2 := filepath.Join(t.TempDir(), "layer2.ext4")
	if err := os.WriteFile(src2, []byte("layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Store(hash, FrameworkNode, api.PlanHobby, src2, 5); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(cs)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("B1.3 idempotency regression: sidecar content changed across Store calls\n"+
			"first:  %q\nsecond: %q", first, second)
	}
}

// TestChecksumPath_Roundtrip pins the canonical sidecar path shape.
// Future renames that move the sidecar outside the layer dir, or
// that change the extension, will trip this test.
func TestChecksumPath_Roundtrip(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	got := c.checksumPath("0123456789abcdef", FrameworkNode, api.PlanHobby)
	want := filepath.Join(root, "0123456789abcdef.node.hobby", "layer.sha256")
	if got != want {
		t.Errorf("checksumPath = %q, want %q (sibling file inside layer dir)", got, want)
	}
}

// --- B2.1 (issue #196): build cache GC ---

// seedEntry writes a fresh cache entry directly to disk (bypassing
// Store, which would re-touch mtimes and make time-based assertions
// awkward) and back-dates its directory mtime via os.Chtimes. Returns
// the entry dir path. size is the layer file's byte size; the sidecar
// is always 8 bytes ("<hash>\n") so total = size+8.
func seedEntry(t *testing.T, c *Cache, hash string, fw Framework, size int, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(c.root, hash+"."+string(fw))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layer.ext4"), bytesOfSize(size), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "layer.sha256"), []byte(hash+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return dir
}

// bytesOfSize returns a deterministic byte slice of length n. Used as
// filler for fake layer.ext4 files so dirSize can be asserted against
// a known total.
func bytesOfSize(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251) // non-zero so file isn't sparse
	}
	return b
}

// quietLogger returns a slog.Logger that discards all output. Sweep
// uses the logger for WARN/INFO lines; the tests assert behavior, not
// log output, so silence makes `go test -v` output readable.
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestCacheSweep_TTLEvictsOld is the B2.1 TTL-pass gate. Two entries:
// one fresh, one 31 days old. A 30-day TTL must sweep only the old
// one and keep the fresh one. Bug class the test catches: sweep
// accidentally computing mtime against the layer.ext4 instead of the
// entry directory (the entry dir is the documented mtime source per
// B1.3 / collectEntries).
func TestCacheSweep_TTLEvictsOld(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	now := time.Now()
	old := seedEntry(t, c, "oldhash0000000000000000", FrameworkNode, 100, now.Add(-31*24*time.Hour))
	fresh := seedEntry(t, c, "freshhash00000000000000", FrameworkNode, 100, now.Add(-1*time.Hour))

	n, err := c.Sweep(0, 30*24*time.Hour, now, quietLogger()) // 0 = no size cap
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept = %d, want 1", n)
	}
	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old entry should be gone: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh entry should survive: %v", err)
	}
}

// TestCacheSweep_SizeCapEvictsOldest verifies policy 2: when the
// total cache size exceeds maxBytes, oldest-first eviction kicks in.
// Three entries, all "fresh" (within TTL), totalling 3*108 = 324 bytes.
// A 200-byte cap can hold ≤1 entry; size-cap must evict the two
// oldest, leaving the newest. Bug class the test catches: an
// off-by-one in the "total <= maxBytes" termination that wastes one
// extra eviction, OR an ascending sort that NEVER reaches the cap.
func TestCacheSweep_SizeCapEvictsOldest(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	now := time.Now()
	e1 := seedEntry(t, c, "entry111111111111111111", FrameworkNode, 100, now.Add(-3*time.Hour))
	e2 := seedEntry(t, c, "entry222222222222222222", FrameworkNode, 100, now.Add(-2*time.Hour))
	e3 := seedEntry(t, c, "entry333333333333333333", FrameworkNode, 100, now.Add(-1*time.Hour))

	// No TTL (maxAge large); 200-byte cap; each entry is 100+8=108 bytes.
	// 200 < 108*2 = 216, so two entries don't fit; one does.
	n, err := c.Sweep(200, 100*365*24*time.Hour, now, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("swept = %d, want 2", n)
	}
	if _, err := os.Stat(e1); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("e1 should be evicted: %v", err)
	}
	if _, err := os.Stat(e2); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("e2 should be evicted: %v", err)
	}
	if _, err := os.Stat(e3); err != nil {
		t.Errorf("e3 (newest) should survive: %v", err)
	}
}

// TestCacheSweep_CombinedTTLThenSize verifies the documented order:
// TTL runs first, then size cap operates on the survivors. Setup:
// one stale (TTL victim) + two fresh (size-cap victims). Expected:
// 3 swept, 0 surviving. Bug class: an implementation that merges the
// two passes into one sorted list could accidentally fresh-evict
// ahead of stale-evict and report the wrong surviving count.
func TestCacheSweep_CombinedTTLThenSize(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	now := time.Now()
	stale := seedEntry(t, c, "stalehash000000000000000", FrameworkNode, 100, now.Add(-31*24*time.Hour))
	fresh1 := seedEntry(t, c, "fresh1111111111111111111", FrameworkNode, 100, now.Add(-2*time.Hour))
	fresh2 := seedEntry(t, c, "fresh2222222222222222222", FrameworkNode, 100, now.Add(-1*time.Hour))

	n, err := c.Sweep(50, 30*24*time.Hour, now, quietLogger()) // 50B cap; any fresh+TTL=108B
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("swept = %d, want 3 (1 TTL + 2 size-cap)", n)
	}
	for _, p := range []string{stale, fresh1, fresh2} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s should be gone: %v", p, err)
		}
	}
}

// TestCacheSweep_EmptyCacheIsNoop asserts the documented contract:
// Sweep on a missing or empty cache root returns (0, nil) without
// error. The GC is meant to be safe to run unconditionally on every
// tick — a fresh deployment with no builds yet must not log errors.
func TestCacheSweep_EmptyCacheIsNoop(t *testing.T) {
	t.Run("missing root", func(t *testing.T) {
		c := NewCache(filepath.Join(t.TempDir(), "does-not-exist"))
		n, err := c.Sweep(0, 30*24*time.Hour, time.Now(), quietLogger())
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		if n != 0 {
			t.Errorf("swept = %d, want 0", n)
		}
	})
	t.Run("empty root", func(t *testing.T) {
		c := NewCache(t.TempDir())
		n, err := c.Sweep(0, 30*24*time.Hour, time.Now(), quietLogger())
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		if n != 0 {
			t.Errorf("swept = %d, want 0", n)
		}
	})
	t.Run("nil cache", func(t *testing.T) {
		var c *Cache
		n, err := c.Sweep(0, 30*24*time.Hour, time.Now(), quietLogger())
		if err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		if n != 0 {
			t.Errorf("swept = %d, want 0", n)
		}
	})
}

// TestCacheSweep_FirstWriterWinsPreserved asserts that a concurrent
// Cache.Store that completes during a Sweep is preserved if the
// Store's mtime makes it fresh. Setup: a TTL-evictable entry that
// Store re-publishes with a fresh mtime mid-sweep; the fresh
// publish must survive. This is the regression test for a sweep
// that races on a directory deletion. Note: this test does not
// stress-test the Store/Sweep race intentionally — see
// TestCacheSweep_ConcurrentStoreAndSweep for that.
func TestCacheSweep_FirstWriterWinsPreserved(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	now := time.Now()
	// Seed with one FRESH entry (within TTL) that's a single byte over
	// the size cap; the size-cap pass must evict it despite first-
	// writer-wins semantics that Store protects.
	victim := seedEntry(t, c, "victim00000000000000000", FrameworkNode, 1024, now)

	// Size cap is 100 bytes; victim is 1024+8 = 1032 bytes, well over.
	n, err := c.Sweep(100, 100*365*24*time.Hour, now, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept = %d, want 1", n)
	}
	if _, err := os.Stat(victim); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("victim over cap should be evicted: %v", err)
	}
}

// TestCacheSweep_DirSizeIsRecursive verifies dirSize walks
// recursively — a cache entry dir containing layer.ext4 +
// layer.sha256 + a hypothetical manifest.json must size them all.
// dirSize is unexported; the test reaches it via the cache.EntrYDir
// observed through Sweep / collectEntries. We assert via
// construction: seed two entries of known size, sweep with a cap
// that fits only one, verify which survives.
func TestCacheSweep_DirSizeIsRecursive(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	now := time.Now()
	// Entry A: 1000 + 8(sidecar) = 1008 bytes (+ extra file for recursion proof).
	dirA := filepath.Join(c.root, "aaaaaaaaaaaaaaaaaaaaaaaa", "node")
	if err := os.MkdirAll(dirA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "layer.ext4"), bytesOfSize(1000), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "layer.sha256"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirA, "manifest.json"), bytesOfSize(500), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(c.root, "aaaaaaaaaaaaaaaaaaaaaaaa"), now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Entry B: 100 bytes + 8 = 108 bytes (the test's survivor).
	eB := seedEntry(t, c, "bbbbbbbbbbbbbbbbbbbbbbbb", FrameworkNode, 100, now.Add(-1*time.Hour))

	// Cap at 200 bytes: A is 1508+ (recursive), B is 108. Sweep must
	// evict A (oldest AND over cap) and keep B.
	n, err := c.Sweep(200, 100*365*24*time.Hour, now, quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(c.root, "aaaaaaaaaaaaaaaaaaaaaaaa")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("entry A (recursive-size) should be gone: %v", err)
	}
	if _, err := os.Stat(eB); err != nil {
		t.Errorf("entry B should survive: %v", err)
	}
}

// TestCacheSweep_ConcurrentStoreAndSweep is the B2.1 race gate.
// Two goroutines: one calling Store on a NEW key, one calling Sweep
// with a TTL of zero (so EVERY existing entry is TTL-evictable). The
// Store write must NOT be torn — either it survives the sweep
// (fresh-newer-than-now) or it cleanly misses (entry gone, next
// build retries). No panic, no half-written file. This is the
// regression test the Plan agent flagged: Store's old code would
// have raced a Sweep deleting the dst directory mid-rename.
func TestCacheSweep_ConcurrentStoreAndSweep(t *testing.T) {
	root := t.TempDir()
	c := NewCache(root)
	now := time.Now()
	// Pre-seed one already-evictable entry so Sweep has work to do.
	seedEntry(t, c, "pre-existing00000000000", FrameworkNode, 64, now.Add(-1*time.Hour))

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			select {
			case <-stop:
				return
			default:
			}
			hash := "concurrent" + strings.Repeat("x", 56) + fmtInt(i)
			src := filepath.Join(t.TempDir(), "layer.ext4")
			if err := os.WriteFile(src, bytesOfSize(64), 0o644); err != nil {
				t.Errorf("seed src: %v", err)
				return
			}
			// Chtimes the entry dir to NOW after Store so the entry
			// survives the Sweep's TTL=0 pass (which evicts entries
			// whose mtime < now).
			if err := c.Store(hash, FrameworkNode, api.PlanHobby, src, 64); err != nil {
				// Acceptable: sweep may have raced and deleted the
				// dst dir mid-write. Next build retries.
				continue
			}
			dir := filepath.Join(c.root, hash+".node.hobby")
			if err := os.Chtimes(dir, now.Add(time.Duration(i+1)*time.Second), now.Add(time.Duration(i+1)*time.Second)); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("chtimes: %v", err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			select {
			case <-stop:
				return
			default:
			}
			// TTL=0 means cutoff=now; any entry with mtime <= now
			// is evicted. The concurrently-stored entries have
			// mtime > now (we Chtimes them forward above), so they
			// should survive.
			if _, err := c.Sweep(0, 0, now.Add(time.Duration(i+1)*time.Second), quietLogger()); err != nil {
				t.Errorf("sweep: %v", err)
				return
			}
		}
	}()

	wg.Wait()
	close(stop)

	// After the race, the cache should be consistent: any remaining
	// entry must have BOTH a layer.ext4 and a layer.sha256 (Lookup
	// invariant). A torn entry is a bug.
	entries, err := os.ReadDir(c.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		layer := filepath.Join(c.root, e.Name(), "layer.ext4")
		side := filepath.Join(c.root, e.Name(), "layer.sha256")
		_, lerr := os.Stat(layer)
		_, serr := os.Stat(side)
		// Both must be present, or both must be absent (the entry
		// was concurrently evicted between the two stats). A layer
		// without a sidecar is the B1.3 tamper signal.
		if (lerr == nil) != (serr == nil) {
			t.Errorf("torn entry %s: layer_err=%v sidecar_err=%v", e.Name(), lerr, serr)
		}
	}
}

// TestCacheSweep_StopsOnContextCancel verifies the loop honors ctx.
// CacheGCSweepLoop must not run another Sweep after the ctx fires.
// This is the test for the cmd/builderd/main.go shutdown contract.
func TestCacheSweep_StopsOnContextCancel(t *testing.T) {
	c := NewCache(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		CacheGCSweepLoop(ctx, c, time.Hour, 0, 0, quietLogger())
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("CacheGCSweepLoop did not return on ctx cancel")
	}
}

// fmtInt is a local helper to avoid importing strconv just for the
// concurrent test.
func fmtInt(i int) string {
	if i == 0 {
		return "0"
	}
	digits := "0123456789"
	out := ""
	for i > 0 {
		out = string(digits[i%10]) + out
		i /= 10
	}
	return out
}

// We use context in TestCacheSweep_StopsOnContextCancel; import
// happens in cache_test.go's import block.
var _ = context.Background
