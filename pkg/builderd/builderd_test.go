package builderd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeNotifier records every Notify call. Used to assert build_log fan-out
// and snapshot_prime emission.
type fakeNotifier struct{ calls []notifyCall }

type notifyCall struct {
	channel, payload string
}

func (f *fakeNotifier) Notify(_ context.Context, channel, payload string) error {
	f.calls = append(f.calls, notifyCall{channel, payload})
	return nil
}

// fakeVM is the test VM driver. It returns the configured result, optionally
// failing. The result's OCIImage is what ProcessOne stamps onto the
// deployment row.
type fakeVM struct {
	out        BuildOutcome
	spawnErr   error
	waitErr    error
	spawnCalls int
	waitCalls  int
	handle     BuildHandle
}

func (f *fakeVM) Spawn(_ context.Context, _ VMRequest) (BuildHandle, error) {
	f.spawnCalls++
	if f.handle.Instance == "" {
		f.handle = BuildHandle{Instance: "build-test", BuildID: "test", TimeoutSec: 30}
	}
	return f.handle, f.spawnErr
}

func (f *fakeVM) WaitForCompletion(_ context.Context, _ BuildHandle) (BuildOutcome, error) {
	f.waitCalls++
	return f.out, f.waitErr
}

// seedDeployment creates an account + app + source-tarball deployment with a
// build row in the queued state. Returns the buildID and the deployment ID.
func seedDeployment(t *testing.T, store state.Store, source string) (string, string, string) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), "u@example.com", "pro")
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "src-app", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  source,
		SourceBytes: 100,
		LogPath:     filepath.Join(t.TempDir(), "build.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := store.CreateBuild(context.Background(), dep.ID, state.DeploymentKindTarball, 100, dep.LogPath)
	if err != nil {
		t.Fatal(err)
	}
	return build.ID, dep.ID, app.ID
}

func TestProcessOne_CacheHitSkipsSpawn(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, depID, appID := seedDeployment(t, store, src)

	// Pre-populate the cache so the lookup hits.
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	layerPath := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(layerPath, []byte("pre-cached layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(src)
	if err := c.Store(hash, FrameworkNode, layerPath, 18); err != nil {
		t.Fatal(err)
	}

	fvm := &fakeVM{} // would panic if called — proves the spawn was skipped.
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, c, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	res, err := b.ProcessOne(context.Background(), buildID)
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !res.CacheHit {
		t.Error("expected cache hit")
	}
	if fvm.spawnCalls != 0 {
		t.Errorf("VM spawn was called %d times, want 0 (cache hit)", fvm.spawnCalls)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.RootfsPath == "" {
		t.Error("rootfs_path not stamped on deployment")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded", build.Status)
	}
	bootFound := false
	for _, c := range notif.calls {
		if c.channel == db.NotifySnapshotBoot &&
			contains(c.payload, appID) &&
			contains(c.payload, depID) {
			bootFound = true
		}
	}
	if !bootFound {
		t.Errorf("expected snapshot_boot notification; got %v", notif.calls)
	}
}

func TestProcessOne_VMSpawnSucceedsAndStamps(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	// Empty file but a tarball-shaped name — the detector will fail on it
	// because there's no gzip header. Use a real tarball instead.
	srcTar := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, srcTar, []string{"package.json", "index.js"})

	buildID, depID, _ := seedDeployment(t, store, srcTar)
	_ = src

	// VM produces a layer at /tmp/somewhere/layer.ext4
	out := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(out, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: out, ExitCode: 0, LogTailBytes: 14}}
	notif := &fakeNotifier{}
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	b := New(store, notif, fvm, c, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if fvm.spawnCalls != 1 {
		t.Errorf("VM spawn was called %d times, want 1", fvm.spawnCalls)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.RootfsBytes != 14 {
		t.Errorf("rootfs_bytes = %d, want 14", dep.RootfsBytes)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded", build.Status)
	}
	// Cache should have been populated.
	hash, _ := hashFile(srcTar)
	if _, ok := c.Lookup(hash, FrameworkNode); !ok {
		t.Error("expected cache populated after successful build")
	}
}

func TestProcessOne_OOMExitClassified(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, _, _ := seedDeployment(t, store, src)
	fvm := &fakeVM{out: BuildOutcome{OCIImage: "/dev/null", ExitCode: 137, FailureClass: "FailureOOM"}} // guest-init captures this
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected error on non-zero VM exit")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildFailed {
		t.Errorf("build status = %s, want failed", build.Status)
	}
	if build.FailureClass != state.FailureOOM {
		t.Errorf("failure_class = %s, want oom", build.FailureClass)
	}
}

func TestProcessOne_VMSpawnErrorIsInfra(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, _, _ := seedDeployment(t, store, src)
	fvm := &fakeVM{spawnErr: errors.New("vmmd down")}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected error from VM spawn")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.FailureClass != state.FailureInfra {
		t.Errorf("failure_class = %s, want infra", build.FailureClass)
	}
}

func TestProcessOne_NotMetalStubError(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, _, _ := seedDeployment(t, store, src)
	notif := &fakeNotifier{}
	// nil VM driver → orchestrator returns ErrNotMetal + marks infra.
	b := New(store, notif, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if !errors.Is(err, ErrNotMetal) {
		t.Fatalf("expected ErrNotMetal, got %v", err)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildFailed {
		t.Errorf("build status = %s, want failed", build.Status)
	}
}

func TestProcessOne_UnknownFrameworkFails(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	// No package.json, no Dockerfile, no requirements — detector errors.
	makeTarballWithName(t, src, []string{"README.md"})

	buildID, _, _ := seedDeployment(t, store, src)
	notif := &fakeNotifier{}
	b := New(store, notif, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected detector failure")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.FailureClass != state.FailureUserError {
		t.Errorf("failure_class = %s, want user_error", build.FailureClass)
	}
}

// makeTarballWithName is a small wrapper around the makeTarball in
// detect_test.go so this file's tests don't have to redeclare the helper.
func makeTarballWithName(t *testing.T, path string, names []string) {
	t.Helper()
	makeTarball(t, path, names)
}

// contains is a tiny substring helper.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
