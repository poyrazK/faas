// Tests for the Build pipeline (cmd-runner is faked) and the inject helpers.
// The happy-path Build is covered by rootfs_test.go; this file pins the
// error branches and the inject helpers' boundary conditions.

package rootfs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/storage"
)

func TestInjectManifest_WritesCanonicalJSON(t *testing.T) {
	staging := t.TempDir()
	m := api.AppManifest{
		Entrypoint: []string{"/app/server"},
		Env:        map[string]string{"X": "y"},
		Port:       8080,
	}
	if err := InjectManifest(staging, m); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(staging, "etc", "faas", "app.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("manifest not at expected path %q: %v", path, err)
	}
	if !bytes.Contains(b, []byte("entrypoint")) {
		t.Errorf("manifest content missing entrypoint: %s", b)
	}
	if !bytes.Contains(b, []byte("8080")) {
		t.Errorf("manifest content missing Port: %s", b)
	}
}

func TestInjectGuestInit_HappyPath(t *testing.T) {
	staging := t.TempDir()
	src := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(src, []byte("guest-init binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InjectGuestInit(staging, src); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(staging, "sbin", "init")
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("init not at expected path: %v", err)
	}
	if st.Size() == 0 {
		t.Error("init file is empty")
	}
	if st.Mode().Perm()&0o111 == 0 {
		t.Errorf("init not executable: mode %o", st.Mode().Perm())
	}
}

func TestInjectGuestInit_EmptyPath(t *testing.T) {
	if err := InjectGuestInit(t.TempDir(), ""); err == nil {
		t.Error("empty guest-init path should error")
	}
}

func TestInjectGuestInit_MissingSource(t *testing.T) {
	if err := InjectGuestInit(t.TempDir(), "/no/such/file"); err == nil {
		t.Error("missing source should error")
	}
}

func TestBuild_UnknownPlan(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "apps/x/y.ext4",
		Plan:       "nope",
	})
	if err == nil {
		t.Fatal("unknown plan should error")
	}
	if !strings.Contains(err.Error(), "unknown plan") {
		t.Errorf("error %q should mention unknown plan", err.Error())
	}
}

func TestBuild_InvalidManifest(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "apps/x/y.ext4",
		Plan:       api.PlanFree,
		// Empty Entrypoint → Validate() fails.
	})
	if err == nil {
		t.Fatal("invalid manifest should error")
	}
}

func TestBuild_MkfsFailure(t *testing.T) {
	src := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(src, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &failRunner{err: os.ErrNotExist}
	b := NewBuilder(run)
	_, err := b.Build(context.Background(), BuildInput{
		Storage:       newTestStorage(t),
		StorageKey:    "apps/x/y.ext4",
		Plan:          api.PlanFree,
		GuestInitPath: src,
		Manifest:      api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err == nil {
		t.Fatal("mkfs failure should propagate")
	}
	if !strings.Contains(err.Error(), "mkfs") {
		t.Errorf("error %q should mention mkfs", err.Error())
	}
}

// TestBuild_RejectsMissingOutputTarget covers the new validation:
// every BuildInput must specify exactly one of {Storage, OutImage}.
// Without it, a misconfigured caller would silently drop the produced
// ext4.
func TestBuild_RejectsMissingOutputTarget(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Plan:     api.PlanFree,
		Manifest: api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err == nil {
		t.Fatal("missing output target should error")
	}
	if !strings.Contains(err.Error(), "neither Storage nor OutImage") {
		t.Errorf("error %q should mention the validation rule", err.Error())
	}
}

// TestBuild_RejectsBothOutputTargets covers the inverse: specifying
// both would let one path silently shadow the other. The validator
// surfaces this loudly.
func TestBuild_RejectsBothOutputTargets(t *testing.T) {
	b := NewBuilder(&fakeRunner{})
	_, err := b.Build(context.Background(), BuildInput{
		Storage:    newTestStorage(t),
		StorageKey: "apps/x/y.ext4",
		OutImage:   filepath.Join(t.TempDir(), "y.ext4"),
		Plan:       api.PlanFree,
		Manifest:   api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err == nil {
		t.Fatal("both output targets should error")
	}
}

// TestBuild_PublishesViaStorage exercises the production Storage
// path: Build produces a tmp ext4 via the fakeRunner (which writes
// fake ext4 bytes into the tmp path, mimicking a real mkfs), then
// Put's it under StorageKey. After Build returns, Get(StorageKey)
// returns the same bytes. This is the test that proves the
// publishExt4 → Storage.Put wiring is correct end-to-end without
// invoking a real mkfs.
func TestBuild_PublishesViaStorage(t *testing.T) {
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	be := newTestStorage(t)
	run := &mkfsFakeRunner{fill: []byte("FAKE-EXT4-CONTENT")}
	b := NewBuilder(run)
	res, err := b.Build(context.Background(), BuildInput{
		Storage:       be,
		StorageKey:    "apps/slug/dep.ext4",
		Plan:          api.PlanFree,
		GuestInitPath: gi,
		Manifest:      api.AppManifest{Entrypoint: []string{"/app/server"}},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.ImageKey != "apps/slug/dep.ext4" {
		t.Errorf("ImageKey = %q, want %q", res.ImageKey, "apps/slug/dep.ext4")
	}
	if res.ImagePath != "" {
		t.Errorf("ImagePath = %q, want empty", res.ImagePath)
	}
	// Storage must hold the published ext4 at the requested key with
	// the same bytes the runner wrote.
	rc, err := be.Get(context.Background(), "apps/slug/dep.ext4")
	if err != nil {
		t.Fatalf("storage Get after build: %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, run.fill) {
		t.Fatalf("content mismatch: got %q, want %q", got, run.fill)
	}
}

// TestBuild_LegacyOutImageStillWorks covers the deprecation path:
// an existing caller that still passes OutImage keeps working. The
// integration test (build_integration_test.go) relies on this; new
// callers should use Storage + StorageKey.
func TestBuild_LegacyOutImageStillWorks(t *testing.T) {
	gi := filepath.Join(t.TempDir(), "guest-init")
	if err := os.WriteFile(gi, []byte("INIT"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &fakeRunner{}
	b := NewBuilder(run)
	out := filepath.Join(t.TempDir(), "layer.ext4")
	res, err := b.Build(context.Background(), BuildInput{
		GuestInitPath: gi,
		Manifest:      api.AppManifest{Entrypoint: []string{"node", "x"}},
		Plan:          api.PlanHobby,
		OutImage:      out,
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.ImagePath != out {
		t.Errorf("ImagePath = %q, want %q", res.ImagePath, out)
	}
	// The legacy mkfs argv path must contain OutImage.
	if !containsString(run.argv, out) {
		t.Errorf("argv %v must contain %q (legacy path)", run.argv, out)
	}
}

type failRunner struct{ err error }

func (f *failRunner) Run(_ context.Context, _ []string) error { return f.err }

// newTestStorage builds a LocalStorageBackend rooted at t.TempDir().
// Used by tests that need a real StorageBackend without dragging in
// the full StorageBackend suite.
func newTestStorage(t *testing.T) storage.StorageBackend {
	t.Helper()
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocalStorageBackend: %v", err)
	}
	return be
}

// writeGzTar writes a gzipped tar with one regular file at path
// `name` containing `body`. Used by the B3.7 cap tests so a
// synthetic tarball can be handed to ApplyTarball.
func writeGzTar(t *testing.T, path, name string, body []byte) {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	hdr := &tar.Header{
		Name: name,
		Mode: 0o644,
		Size: int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar.WriteHeader: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("tar.Write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar.Close: %v", err)
	}
	var gz bytes.Buffer
	gzw := gzip.NewWriter(&gz)
	if _, err := gzw.Write(raw.Bytes()); err != nil {
		t.Fatalf("gzip.Write: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("gzip.Close: %v", err)
	}
	if err := os.WriteFile(path, gz.Bytes(), 0o644); err != nil {
		t.Fatalf("write tarball: %v", err)
	}
}

// TestApplyTarball_RespectsCap — issue #197 B3.7. A tarball whose
// single entry's declared size exceeds capBytes must be rejected
// with *ErrTarballExceedsCap before the file is written to disk.
// The cap is the cumulative byte total, not per-entry; per-entry
// is enforced by io.CopyN in applyEntry.
func TestApplyTarball_RespectsCap(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	// 1 MiB body under a 64 KiB cap. The cap is checked against the
	// declared header size, so the actual write doesn't have to
	// run a million bytes through io.CopyN.
	writeGzTar(t, tarball, "handler.js", bytes.Repeat([]byte("x"), 1024*1024))

	const capBytes = 64 * 1024
	err := ApplyTarball(staging, tarball, capBytes)
	if err == nil {
		t.Fatalf("ApplyTarball accepted 1 MiB body under %d cap", capBytes)
	}
	var capErr *ErrTarballExceedsCap
	if !errors.As(err, &capErr) {
		t.Fatalf("err = %v (%T); want *ErrTarballExceedsCap", err, err)
	}
	if capErr.CapBytes != capBytes {
		t.Errorf("CapBytes = %d; want %d", capErr.CapBytes, capBytes)
	}
	if capErr.EntryBytes != 1024*1024 {
		t.Errorf("EntryBytes = %d; want %d", capErr.EntryBytes, 1024*1024)
	}
}

// TestApplyTarball_UnderCapAccepts — the inverse: a small tarball
// under the cap must unpack cleanly.
func TestApplyTarball_UnderCapAccepts(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	writeGzTar(t, tarball, "handler.js", []byte("console.log('hi')"))
	if err := ApplyTarball(staging, tarball, 1024*1024); err != nil {
		t.Fatalf("ApplyTarball under-cap: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(staging, "app", "handler.js"))
	if err != nil {
		t.Fatalf("read handler.js: %v", err)
	}
	if string(body) != "console.log('hi')" {
		t.Errorf("body = %q; want %q", body, "console.log('hi')")
	}
}

// TestApplyTarball_ZeroCapSkipsGate — passing capBytes=0 keeps the
// legacy unbounded behavior. The legacy callers (tests, internal
// callers without a plan context) must not regress.
func TestApplyTarball_ZeroCapSkipsGate(t *testing.T) {
	dir := t.TempDir()
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	tarball := filepath.Join(dir, "src.tar.gz")
	writeGzTar(t, tarball, "handler.js", bytes.Repeat([]byte("y"), 16*1024))
	if err := ApplyTarball(staging, tarball, 0); err != nil {
		t.Fatalf("ApplyTarball cap=0: %v", err)
	}
}
