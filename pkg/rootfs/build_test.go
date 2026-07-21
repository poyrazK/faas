// Tests for the Build pipeline (cmd-runner is faked) and the inject helpers.
// The happy-path Build is covered by rootfs_test.go; this file pins the
// error branches and the inject helpers' boundary conditions.

package rootfs

import (
	"bytes"
	"context"
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
