package deploycontroller

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/releasebundle"
)

type fakeRuntime struct {
	calls        []string
	migrateErr   error
	restartErr   error
	healthyErr   error
	healthyCalls int
	activateErr  error
}

func (f *fakeRuntime) Preflight(_ context.Context, _ releasebundle.Manifest, _ string) error {
	f.calls = append(f.calls, "preflight")
	return nil
}
func (f *fakeRuntime) Migrate(_ context.Context, _ releasebundle.Manifest, _, _ string) error {
	f.calls = append(f.calls, "migrate")
	return f.migrateErr
}
func (f *fakeRuntime) Activate(_ context.Context, root string) error {
	f.calls = append(f.calls, "activate:"+root)
	return f.activateErr
}
func (f *fakeRuntime) Restart(_ context.Context, _ releasebundle.Manifest) error {
	f.calls = append(f.calls, "restart")
	return f.restartErr
}
func (f *fakeRuntime) Healthy(_ context.Context, _ releasebundle.Manifest) error {
	f.calls = append(f.calls, "healthy")
	f.healthyCalls++
	if f.healthyCalls == 1 {
		return f.healthyErr
	}
	return nil
}

func TestDeployActivatesVerifiedRelease(t *testing.T) {
	root := t.TempDir()
	releaseRoot := makeRelease(t, root, "new")
	current := filepath.Join(root, "current")
	if err := os.Symlink(filepath.Join(root, "old"), current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	controller := newController(t, root, current, runtime)

	if err := controller.Deploy(context.Background(), "new"); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if !strings.Contains(releaseRoot, "new") {
		t.Fatalf("releaseRoot = %q", releaseRoot)
	}
	want := []string{"preflight", "migrate", "activate:" + releaseRoot, "restart", "healthy"}
	if strings.Join(runtime.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", runtime.calls, want)
	}
}

func TestDeployAcceptsSBOMBaselineInCurrentRelease(t *testing.T) {
	root := t.TempDir()
	newRelease := makeRelease(t, root, "new")
	old := makeRelease(t, root, "old")
	if err := os.WriteFile(filepath.Join(old, "sbom-baseline.json"), []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(old, current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	controller := newController(t, root, current, runtime)

	if err := controller.Deploy(context.Background(), "new"); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	if got, err := os.Readlink(current); err != nil || got != newRelease {
		t.Fatalf("current = %q, %v; want %q", got, err, newRelease)
	}
}

func TestDeployRollsBackOnHealthFailure(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "new")
	old := makeRelease(t, root, "old")
	if err := os.WriteFile(filepath.Join(old, "sbom-baseline.json"), []byte(`{"counts":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(old, current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{healthyErr: errors.New("gateway not ready")}
	controller := newController(t, root, current, runtime)

	err := controller.Deploy(context.Background(), "new")
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("Deploy error = %v, want rollback error", err)
	}
	want := []string{"preflight", "migrate", "activate:" + filepath.Join(root, "new"), "restart", "healthy", "activate:" + old, "restart", "healthy"}
	if strings.Join(runtime.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", runtime.calls, want)
	}
}

func TestDeployRollsBackWhenCurrentSymlinkIsRelative(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "new")
	old := makeRelease(t, filepath.Join(root, "releases"), "old")
	current := filepath.Join(root, "current")
	if err := os.Symlink(filepath.Join("releases", "old"), current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{healthyErr: errors.New("gateway not ready")}
	controller := newController(t, root, current, runtime)

	err := controller.Deploy(context.Background(), "new")
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("Deploy error = %v, want rollback error", err)
	}
	want := []string{"preflight", "migrate", "activate:" + filepath.Join(root, "new"), "restart", "healthy", "activate:" + old, "restart", "healthy"}
	if strings.Join(runtime.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", runtime.calls, want)
	}
}

func TestDeployRejectsExistingCurrentReleaseWithoutVerifiedRollback(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "new")
	old := filepath.Join(root, "old")
	if err := os.MkdirAll(filepath.Join(old, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "manifest.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(root, "current")
	if err := os.Symlink(old, current); err != nil {
		t.Fatal(err)
	}
	runtime := &fakeRuntime{}
	controller := newController(t, root, current, runtime)

	err := controller.Deploy(context.Background(), "new")
	if err == nil || !strings.Contains(err.Error(), "not rollback-capable") {
		t.Fatalf("Deploy error = %v, want rollback-capable preflight error", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime calls = %v, want no calls before preflight", runtime.calls)
	}
}

func TestDeployStopsBeforeActivationWhenMigrationFails(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "new")
	runtime := &fakeRuntime{migrateErr: errors.New("incompatible schema")}
	controller := newController(t, root, filepath.Join(root, "current"), runtime)

	if err := controller.Deploy(context.Background(), "new"); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("Deploy error = %v, want migration error", err)
	}
	if len(runtime.calls) != 2 || runtime.calls[0] != "preflight" || runtime.calls[1] != "migrate" {
		t.Fatalf("calls = %v, want only migrate", runtime.calls)
	}
}

func TestDeployRejectsConcurrentDeployment(t *testing.T) {
	root := t.TempDir()
	makeRelease(t, root, "new")
	lockPath := filepath.Join(root, "deploy.lock")
	lock, err := acquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	controller, err := New(Config{
		ReleasesRoot: root,
		CurrentPath:  filepath.Join(root, "current"),
		LockPath:     lockPath,
	}, &fakeRuntime{})
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.Deploy(context.Background(), "new"); err == nil || !strings.Contains(err.Error(), "another deployment") {
		t.Fatalf("Deploy error = %v, want lock error", err)
	}
}

func TestDeployRejectsTamperedRelease(t *testing.T) {
	root := t.TempDir()
	release := makeRelease(t, root, "new")
	if err := os.WriteFile(filepath.Join(release, "bin", "apid"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	controller := newController(t, root, filepath.Join(root, "current"), &fakeRuntime{})
	if err := controller.Deploy(context.Background(), "new"); err == nil || !strings.Contains(err.Error(), "verify release") {
		t.Fatalf("Deploy error = %v, want verification error", err)
	}
}

func newController(t *testing.T, root, current string, runtime Runtime) *Controller {
	t.Helper()
	controller, err := New(Config{
		ReleasesRoot: root,
		CurrentPath:  current,
		LockPath:     filepath.Join(root, "deploy.lock"),
	}, runtime)
	if err != nil {
		t.Fatal(err)
	}
	return controller
}

func makeRelease(t *testing.T, root, id string) string {
	t.Helper()
	releaseRoot := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(releaseRoot, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "bin", "apid"), []byte(id), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, err := releasebundle.Build(releaseRoot, id, "commit-"+id, "linux/amd64", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := releasebundle.Write(releaseRoot, manifest); err != nil {
		t.Fatal(err)
	}
	return releaseRoot
}
