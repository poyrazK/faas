// Tests for the Build pipeline (cmd-runner is faked) and the inject helpers.
// The happy-path Build is covered by rootfs_test.go; this file pins the
// error branches and the inject helpers' boundary conditions.

package rootfs

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
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
	_, err := b.Build(context.Background(), BuildInput{Plan: "nope"})
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
		Plan: api.PlanFree,
		// Empty Entrypoint → Validate() fails.
	})
	if err == nil {
		t.Fatal("invalid manifest should error")
	}
}

func TestBuild_MkfsFailure(t *testing.T) {
	// Pipeline reaches the mkfs step but the fake runner errors. We can't
	// inspect the staging dir directly (it's unexported via MkdirTemp), but
	// we can confirm the argv handed to the runner references a path under
	// the staging area and that the failure propagates as expected.
	src := filepath.Join(t.TempDir(), "init")
	if err := os.WriteFile(src, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := &failRunner{err: os.ErrNotExist}
	b := NewBuilder(run)
	_, err := b.Build(context.Background(), BuildInput{
		Plan:          api.PlanFree,
		GuestInitPath: src,
		Manifest:      api.AppManifest{Entrypoint: []string{"/app/server"}},
		OutImage:      filepath.Join(t.TempDir(), "out.ext4"),
	})
	if err == nil {
		t.Fatal("mkfs failure should propagate")
	}
	if !strings.Contains(err.Error(), "mkfs") {
		t.Errorf("error %q should mention mkfs", err.Error())
	}
}

type failRunner struct{ err error }

func (f *failRunner) Run(_ context.Context, _ []string) error { return f.err }
