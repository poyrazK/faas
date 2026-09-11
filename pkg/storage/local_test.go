package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLocalBackendPutGetRoundtrip covers the happy path: Put bytes,
// Get them back, exact equality. This is the single test that every
// other test depends on — if it flakes, the rest of the suite is
// suspect.
func TestLocalBackendPutGetRoundtrip(t *testing.T) {
	be := newTestBackend(t)
	ctx := context.Background()
	want := bytes.Repeat([]byte("faas"), 1024) // 4 KiB of known content
	if err := be.Put(ctx, "snap/dep/mem", bytes.NewReader(want)); err != nil {
		t.Fatalf("put: %v", err)
	}
	rc, err := be.Get(ctx, "snap/dep/mem")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestLocalBackendLocalPathResolvesSymlink(t *testing.T) {
	root := t.TempDir()
	be, err := NewLocalStorageBackend(root)
	if err != nil {
		t.Fatalf("new backend: %v", err)
	}
	target := filepath.Join(root, "base", "runner.ext4")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("base"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, "base", "base-amd64.ext4")
	if err := os.Symlink(filepath.Base(target), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, ok, err := be.LocalPath("base/base-amd64.ext4")
	if err != nil || !ok {
		t.Fatalf("LocalPath: path=%q ok=%v err=%v", got, ok, err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	if got != want {
		t.Fatalf("LocalPath = %q, want resolved target %q", got, want)
	}
}

// TestLocalBackendPutOverwrite covers the overwrite path: a second
// Put to the same key replaces the file. Atomicity is exercised here
// (temp+rename) but the success assertion is content-equality.
func TestLocalBackendPutOverwrite(t *testing.T) {
	be := newTestBackend(t)
	ctx := context.Background()
	if err := be.Put(ctx, "k", strings.NewReader("first")); err != nil {
		t.Fatalf("put first: %v", err)
	}
	if err := be.Put(ctx, "k", strings.NewReader("second")); err != nil {
		t.Fatalf("put second: %v", err)
	}
	got := mustReadAll(t, be, "k")
	if got != "second" {
		t.Fatalf("got %q, want %q", got, "second")
	}
}

func TestLocalBackendPutPublishesSharedReadMode(t *testing.T) {
	root := t.TempDir()
	be, err := NewLocalStorageBackend(root)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	path := filepath.Join(root, "base", "runner.ext4.digest")
	for i, content := range []string{"first", "replacement"} {
		if err := be.Put(context.Background(), "base/runner.ext4.digest", strings.NewReader(content)); err != nil {
			t.Fatalf("Put %d: %v", i+1, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat %d: %v", i+1, err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("mode after Put %d = %04o, want 0644", i+1, got)
		}
	}
}

// TestLocalBackendDeleteRemoves covers the success path: Put, Delete,
// Get returns ErrNotFound. The chain is the same as imaged's
// cleanupDeploymentFiles flow.
func TestLocalBackendDeleteRemoves(t *testing.T) {
	be := newTestBackend(t)
	ctx := context.Background()
	if err := be.Put(ctx, "k", strings.NewReader("x")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := be.Delete(ctx, "k"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := be.Get(ctx, "k"); !IsNotFound(err) {
		t.Fatalf("get after delete: want ErrNotFound, got %v", err)
	}
}

// TestLocalBackendDeleteMissingIsNoop covers the idempotency rule
// (Delete on a missing key must NOT error). imaged's cleanup paths
// depend on this — a stray notification that races cleanup must not
// fail the notification handler.
func TestLocalBackendDeleteMissingIsNoop(t *testing.T) {
	be := newTestBackend(t)
	if err := be.Delete(context.Background(), "never-existed"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

// TestLocalBackendGetMissingIsNotFound covers the cold-boot fallback
// contract: a missing key MUST surface as IsNotFound(err) so
// schedd/fcvm callers can branch to cold boot (ADR-005). This is
// the only test that enforces that invariant directly.
func TestLocalBackendGetMissingIsNotFound(t *testing.T) {
	be := newTestBackend(t)
	_, err := be.Get(context.Background(), "missing")
	if err == nil {
		t.Fatalf("get missing: nil err, want ErrNotFound")
	}
	if !IsNotFound(err) {
		t.Fatalf("get missing: IsNotFound=false, err=%v", err)
	}
	// The legacy single-box code in imaged+fcvm uses errors.Is(err,
	// os.ErrNotExist); the wrapper preserves that chain.
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("get missing: errors.Is(err, os.ErrNotExist)=false, err=%v", err)
	}
}

// TestLocalBackendNestedCreatesParentDirs covers the rule that Put
// must MkdirAll the parent. snapshot deps are nested under snap/<dep>
// and imaged's apps/ layout has apps/<slug>/<dep>.ext4 — both rely
// on this guarantee.
func TestLocalBackendNestedCreatesParentDirs(t *testing.T) {
	be := newTestBackend(t)
	if err := be.Put(context.Background(), "snap/dep/mem", strings.NewReader("x")); err != nil {
		t.Fatalf("put nested: %v", err)
	}
	mustReadAll(t, be, "snap/dep/mem")
}

// TestLocalBackendVmstateKeyRoundtrip pins the round-trip contract for
// the issue #121 / ADR-025 axis 2 slice 4 vmstate blob: the canonical
// StorageBackend key `snap/<dep>/vmstate` (sibling of the existing mem
// key) MUST Put + Get bytes with no off-by-one slash, no uppercase
// drift, no file lost on subsequent reads. Default-local wired with
// mem+vmstate under the same backend shares the same nested-create
// guarantee as the mem path.
func TestLocalBackendVmstateKeyRoundtrip(t *testing.T) {
	be := newTestBackend(t)
	const (
		key = "snap/dep-1/vmstate"
		// Realistic vmstate bytes are a small JSON blob (~4 KiB
		// per TestMetalParkWakeCycle telemetry); the test only
		// needs non-empty to exercise the round-trip.
		payload = `{"firecracker":"1.7.0","snapshot":"full","kernel":"vmlinux-6.1.128"}`
	)
	if err := be.Put(context.Background(), key, strings.NewReader(payload)); err != nil {
		t.Fatalf("put vmstate key: %v", err)
	}
	rc, err := be.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get vmstate key: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read vmstate body: %v", err)
	}
	if string(got) != payload {
		t.Errorf("vmstate body mismatch: got %q, want %q", string(got), payload)
	}
}

// TestLocalBackendInvalidKeysRejected is the parameterised invalid-
// key suite. The table format lets us add new rules without adding a
// new test function — every key rule documented on the package
// comment MUST have a row.
func TestLocalBackendInvalidKeysRejected(t *testing.T) {
	be := newTestBackend(t)
	cases := []struct {
		name string
		key  string
	}{
		{"empty", ""},
		{"absolute", "/etc/passwd"},
		{"root_relative", "/foo"},
		{"double_dot", "snap/../mem"},
		{"traversal_inside", "snap/dep/../../escape"},
		{"backslash", `snap\dep\mem`},
		{"nul_byte", "snap/dep\x00/mem"},
		{"cleaned_to_dot", "."},
		{"cleaned_to_empty", "/"},
		{"not_canonical_double_slash", "snap//dep/mem"},
		{"not_canonical_dot_seg", "snap/./dep/mem"},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name+"/Put", func(t *testing.T) {
			if err := be.Put(ctx, tc.key, strings.NewReader("x")); !IsInvalidKey(err) {
				t.Fatalf("put %q: IsInvalidKey=false, err=%v", tc.key, err)
			}
		})
		t.Run(tc.name+"/Get", func(t *testing.T) {
			if _, err := be.Get(ctx, tc.key); !IsInvalidKey(err) {
				t.Fatalf("get %q: IsInvalidKey=false, err=%v", tc.key, err)
			}
		})
		t.Run(tc.name+"/Delete", func(t *testing.T) {
			if err := be.Delete(ctx, tc.key); !IsInvalidKey(err) {
				t.Fatalf("delete %q: IsInvalidKey=false, err=%v", tc.key, err)
			}
		})
	}
}

// TestLocalBackendContextCancel covers ctx propagation: cancel
// mid-Put, the next ctx-aware operation surfaces ctx.Err(). The
// concrete check is that Put returns ctx.Canceled (or wraps it).
func TestLocalBackendContextCancel(t *testing.T) {
	be := newTestBackend(t)
	ctx, cancel := context.WithCancel(context.Background())
	// Force the reader to produce enough data that ctx.Done() has a
	// chance to fire mid-copy. 4 MiB at 256 KiB chunks = 16 polls.
	pr, pw := io.Pipe()
	go func() {
		buf := make([]byte, 64*1024)
		for i := 0; i < 64; i++ {
			if _, err := pw.Write(buf); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
		_ = pw.Close()
	}()
	go func() {
		time.Sleep(2 * time.Millisecond)
		cancel()
	}()
	err := be.Put(ctx, "cancel/key", pr)
	if err == nil {
		t.Fatalf("put with cancelled ctx: nil err")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("put cancelled ctx: errors.Is(err, context.Canceled)=false, err=%v", err)
	}
}

// TestLocalBackendConcurrentPutNoTear covers the atomic-rename rule
// under contention: many concurrent Put calls on distinct keys must
// each produce an intact file. Run with -race for the data-race
// coverage; this test enforces the file-content invariants.
func TestLocalBackendConcurrentPutNoTear(t *testing.T) {
	be := newTestBackend(t)
	ctx := context.Background()
	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("concurrent/%03d/mem", i)
			want := bytes.Repeat([]byte{byte(i)}, 4096)
			if err := be.Put(ctx, key, bytes.NewReader(want)); err != nil {
				t.Errorf("put %s: %v", key, err)
			}
		}()
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("concurrent/%03d/mem", i)
		got := mustReadAllBytes(t, be, key)
		want := bytes.Repeat([]byte{byte(i)}, 4096)
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: content mismatch (%d bytes)", key, len(got))
		}
	}
}

// TestLocalBackendListCovers the LocalArtifactLister interface, which
// imaged's GC consumes via type-assert. The test asserts the returned
// keys are slash-separated and live entirely under the requested
// prefix.
func TestLocalBackendList(t *testing.T) {
	be := newTestBackend(t)
	ctx := context.Background()
	for _, k := range []string{
		"snap/a/mem",
		"snap/a/vmstate",
		"snap/b/mem",
		"base/runtime.ext4",
		"apps/slug/dep.ext4",
	} {
		if err := be.Put(ctx, k, strings.NewReader("x")); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	lister, ok := any(be).(LocalArtifactLister)
	if !ok {
		t.Fatalf("LocalStorageBackend does not implement LocalArtifactLister")
	}
	keys, err := lister.List(ctx, "snap/")
	if err != nil {
		t.Fatalf("list snap/: %v", err)
	}
	want := map[string]bool{
		"snap/a/mem":     true,
		"snap/a/vmstate": true,
		"snap/b/mem":     true,
	}
	if len(keys) != len(want) {
		t.Fatalf("list snap/: got %d keys, want %d (%v)", len(keys), len(want), keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Fatalf("unexpected key %q", k)
		}
		// On Windows the host separator is '\\', so a backslash in a
		// returned key would leak the OS separator into the storage
		// contract. Skip the assertion when host sep is '/' (unix)
		// because the canonical slash form is by definition equal to
		// the host separator there.
		if os.PathSeparator != '/' && strings.ContainsRune(k, os.PathSeparator) {
			t.Fatalf("non-slash separator in key %q", k)
		}
	}
	allKeys, err := lister.List(ctx, "")
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(allKeys) < 5 {
		t.Fatalf("list all: got %d keys, want >=5 (%v)", len(allKeys), allKeys)
	}
	// Invalid prefix is rejected before the walk.
	if _, err := lister.List(ctx, "../escape"); !IsInvalidKey(err) {
		t.Fatalf("list ../escape: IsInvalidKey=false, err=%v", err)
	}
}

// TestNewLocalStorageBackendRootValidation covers the constructor's
// validation: empty root is rejected, and the resolved absolute
// path's directory components (after filepath.Abs + Clean) are
// validated through the same key contract. A literal "/tmp/../escape"
// is collapsed to "/tmp/escape" by filepath.Abs, which is a valid
// path — the deeper traversal check lives in validateKey for keys,
// not for the root (the root is a single trusted config knob, not
// user input).
func TestNewLocalStorageBackendRootValidation(t *testing.T) {
	if _, err := NewLocalStorageBackend(""); !IsInvalidKey(err) {
		t.Fatalf("New(empty): IsInvalidKey=false, err=%v", err)
	}
	be, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("New(tempdir): %v", err)
	}
	if be == nil {
		t.Fatalf("New(tempdir): nil backend")
	}
	if _, err := NewLocalStorageBackend("\x00nul"); !IsInvalidKey(err) {
		t.Fatalf("New(NUL): IsInvalidKey=false, err=%v", err)
	}
}

// newTestBackend returns a LocalStorageBackend rooted at a fresh
// t.TempDir() so each test starts with a clean filesystem. The dir is
// automatically removed when the test ends.
func newTestBackend(t *testing.T) *LocalStorageBackend {
	t.Helper()
	be, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	return be
}

// TestLocalBackendTempFileInDestinationDir is the regression for the
// 2026-07-31 cd-controlplane failure: LocalStorageBackend.Put wrote
// its atomic-rename temp file at `<full> + ".tmp.<pid>.<n>"`, which
// for a nested key like "base/foo" landed at `<root>/foo.tmp.<pid>.<n>`
// — one directory above the final destination. Under systemd's
// ProtectSystem=strict + ReadWritePaths=/srv/fc/base, the parent
// /srv/fc/ is read-only and the temp file creation fails with EROFS,
// crash-looping imaged on every deploy.
//
// We simulate that shape: root is writable only inside a subdir, and
// we assert Put succeeds because the temp file lands in the subdir,
// not at the root. We skip the test when running as root because
// chmod 0555 doesn't restrict root — the test would pass for the
// wrong reason. The CI runner and the prod operator-side deploy
// script both run as unprivileged users, so the skip is local-only.
func TestLocalBackendTempFileInDestinationDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("chmod-based path restriction does not apply to root; manual verify on the box")
	}
	root := t.TempDir()
	// The subdir must exist BEFORE we lock the root down: once
	// the parent is 0555, mkdir is denied. This is the equivalent
	// of the EX44's /srv/fc/base existing at install time, with
	// /srv/fc itself locked down by ProtectSystem=strict.
	sub := filepath.Join(root, "base")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	// Lock down the root so new files cannot be created at <root>/…
	// (subdir's own mode is unaffected; the chmod is on `root` only).
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatalf("chmod root 0555: %v", err)
	}
	// Sanity check: the root is now read-only for this process.
	if err := os.WriteFile(filepath.Join(root, "probe"), []byte("x"), 0o644); err == nil {
		t.Fatalf("root not locked down: write at <root>/probe succeeded (this test is meaningless on this filesystem)")
	}
	be, err := NewLocalStorageBackend(root)
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	// Put for "<subdir>/foo" must succeed: the temp file lives
	// alongside the final destination in the writable subdir.
	if err := be.Put(context.Background(), "base/foo", strings.NewReader("payload")); err != nil {
		t.Fatalf("Put base/foo: %v", err)
	}
	// The final file lives at root/base/foo and has the right content.
	got := mustReadAll(t, be, "base/foo")
	if got != "payload" {
		t.Fatalf("Put then Get: got %q, want %q", got, "payload")
	}
	// No stray .faas-tmp-* file should be left behind — Put's atomic
	// rename must have moved it to the final name.
	entries, err := os.ReadDir(sub)
	if err != nil {
		t.Fatalf("readdir sub: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".faas-tmp-") {
			t.Fatalf("stale temp file left behind: %s", e.Name())
		}
	}
	// And the root itself must remain pristine — no temp file ever
	// landed at <root>/.faas-tmp-*, which was the production bug.
	// We have to relax the perms back so os.ReadDir can list the
	// dir; t.TempDir's cleanup chowns it for us at test end.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Logf("chmod root 0755 (relax for readdir): %v", err)
	}
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir root: %v", err)
	}
	for _, e := range rootEntries {
		if e.Name() == "base" {
			continue
		}
		t.Fatalf("unexpected entry at root: %s (production bug shape: temp file lands at root)", e.Name())
	}
}

// mustReadAll is a test helper that opens the key and returns its
// full content as a string. It fails the test on any error other
// than success — used by tests that want a one-liner content check.
func mustReadAll(t *testing.T, be StorageBackend, key string) string {
	t.Helper()
	rc, err := be.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all %s: %v", key, err)
	}
	return string(b)
}

// mustReadAllBytes is the byte-slice variant of mustReadAll — used
// when the test wants to assert on exact byte equality.
func mustReadAllBytes(t *testing.T, be StorageBackend, key string) []byte {
	t.Helper()
	rc, err := be.Get(context.Background(), key)
	if err != nil {
		t.Fatalf("get %s: %v", key, err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all %s: %v", key, err)
	}
	return b
}

// Reference filepath to keep the import live even when the linter
// would otherwise mark it as unused; some helpers compare against
// host-side paths.
var _ = filepath.Join
