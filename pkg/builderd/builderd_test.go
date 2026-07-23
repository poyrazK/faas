package builderd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
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
// Account defaults to "pro" plan; tests that need a different plan call
// seedDeploymentWithPlan directly.
func seedDeployment(t *testing.T, store state.Store, source string) (string, string, string) {
	t.Helper()
	return seedDeploymentWithPlan(t, store, source, "pro")
}

// seedDeploymentWithPlan is the parameterized form. Used by the
// AppLayerMaxMB cap test below (hobby = 512 MB cap, smaller than the
// 1 MiB filler the fake VM writes).
func seedDeploymentWithPlan(t *testing.T, store state.Store, source, plan string) (string, string, string) {
	t.Helper()
	acct, err := store.CreateAccount(context.Background(), "u@example.com", api.Plan(plan))
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

// seedDeploymentWithSlug is the per-test-call variant for the cache_hit
// subtest (seedBuildForCachePrime). MemStore rejects duplicate account
// emails; vary the slug (and the email) so two consecutive seeds in
// the same test don't collide.
func seedDeploymentWithSlug(t *testing.T, store state.Store, source, slug string) (string, string, string) {
	t.Helper()
	email := fmt.Sprintf("%s@example.com", slug)
	acct, err := store.CreateAccount(context.Background(), email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: slug, RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
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

	buildID, depID, _ := seedDeployment(t, store, src)
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
	// M6 §9.2 closure: a failed build must also flip the deployment row so
	// the dashboard doesn't leave it stuck in DeployBuilding forever.
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status != state.DeployFailed {
		t.Errorf("deployment status = %s, want %s", dep.Status, state.DeployFailed)
	}
	if !contains(dep.Error, "build exited 137") {
		t.Errorf("deployment Error = %q, want substring %q", dep.Error, "build exited 137")
	}
}

// TestProcessOne_FrameworkDetectFailsFlipsDeployment covers the user_error
// path in markFailed — every failure class has to propagate to the owning
// deployment so the dashboard reflects reality, not just the build row.
func TestProcessOne_FrameworkDetectFailsFlipsDeployment(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	// No package.json, no Dockerfile, no requirements — detector errors
	// with user_error (this path pre-dates the VM spawn).
	makeTarballWithName(t, src, []string{"README.md"})

	buildID, depID, _ := seedDeployment(t, store, src)
	notif := &fakeNotifier{}
	b := New(store, notif, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected detector failure")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.FailureClass != state.FailureUserError {
		t.Errorf("build failure_class = %s, want %s", build.FailureClass, state.FailureUserError)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status != state.DeployFailed {
		t.Errorf("deployment status = %s, want %s", dep.Status, state.DeployFailed)
	}
	if !contains(dep.Error, "framework detect") {
		t.Errorf("deployment Error = %q, want substring %q", dep.Error, "framework detect")
	}
}

// TestProcessOne_VMSpawnErrorFlipsDeployment is the infra-failure path —
// builderd couldn't even reach the VM. The deployment must still flip to
// DeployFailed so the customer's next deploy doesn't get blocked waiting on
// a DeployBuilding row.
func TestProcessOne_VMSpawnErrorFlipsDeployment(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, depID, _ := seedDeployment(t, store, src)
	fvm := &fakeVM{spawnErr: errors.New("vmmd socket dead")}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected spawn error")
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status != state.DeployFailed {
		t.Errorf("deployment status = %s, want %s", dep.Status, state.DeployFailed)
	}
	if !contains(dep.Error, "vm spawn") {
		t.Errorf("deployment Error = %q, want substring %q", dep.Error, "vm spawn")
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

// TestProcessOne_AppLayerOverCapFails pins the §4.5 app-layer cap
// enforcement. We point the fake VM at a 600 MB OCI tarball while the
// account is on Hobby (cap = 512 MB). The build must fail with
// failure_class=user_error (this is a customer-content failure, not
// infra) and the deployment must flip to DeployFailed with the cap
// numbers in the error message so the dashboard can surface them.
func TestProcessOne_AppLayerOverCapFails(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	// Hobby = 512 MB cap. Make sure the produced layer is bigger.
	buildID, depID, _ := seedDeploymentWithPlan(t, store, src, "hobby")
	overCapPath := filepath.Join(t.TempDir(), "produced.ext4")
	if err := writeSparse(t, overCapPath, 600*1024*1024); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: overCapPath, ExitCode: 0, LogTailBytes: 14}}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected cap failure")
	}
	if !contains(err.Error(), "exceeds plan cap") {
		t.Errorf("err = %v, want substring %q", err, "exceeds plan cap")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildFailed {
		t.Errorf("build status = %s, want failed", build.Status)
	}
	if build.FailureClass != state.FailureUserError {
		t.Errorf("failure_class = %s, want user_error", build.FailureClass)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status != state.DeployFailed {
		t.Errorf("deployment status = %s, want %s", dep.Status, state.DeployFailed)
	}
	if !contains(dep.Error, "600") || !contains(dep.Error, "512") {
		t.Errorf("deployment Error = %q, want both 600 and 512 in the message", dep.Error)
	}
}

// TestProcessOne_AppLayerAtCapSucceeds is the boundary twin of
// TestProcessOne_AppLayerOverCapFails. Hobby cap = 512 MB; a layer of
// exactly 512 MB must NOT trip the cap. The cap check in builderd.go is
// `>`; this test pins that comparison so a one-byte change to `>=` would
// make the boundary fail and surface in review.
func TestProcessOne_AppLayerAtCapSucceeds(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, _, _ := seedDeploymentWithPlan(t, store, src, "hobby")
	atCapPath := filepath.Join(t.TempDir(), "at-cap.ext4")
	const atCapBytes = int64(512 * 1024 * 1024) // exactly the cap
	if err := writeSparse(t, atCapPath, atCapBytes); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: atCapPath, ExitCode: 0, LogTailBytes: 14}}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne (at cap, must succeed): %v", err)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded (at-cap boundary)", build.Status)
	}
}

// TestProcessOne_AppLayerOneOverCapFails is the matching +/-1 byte case
// for TestProcessOne_AppLayerAtCapSucceeds. Hobby cap = 512 MB; a layer
// of 512 MiB + 1 byte must trip the cap. Together with the at-cap test
// this pins the `>` comparison in builderd.go.
func TestProcessOne_AppLayerOneOverCapFails(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, depID, _ := seedDeploymentWithPlan(t, store, src, "hobby")
	justOverPath := filepath.Join(t.TempDir(), "just-over.ext4")
	const justOverBytes = int64(512*1024*1024) + 1 // cap + 1 byte
	if err := writeSparse(t, justOverPath, justOverBytes); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: justOverPath, ExitCode: 0, LogTailBytes: 14}}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessOne(context.Background(), buildID)
	if err == nil {
		t.Fatal("expected cap failure on cap+1 byte boundary")
	}
	if !contains(err.Error(), "exceeds plan cap") {
		t.Errorf("err = %v, want substring %q", err, "exceeds plan cap")
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status != state.DeployFailed {
		t.Errorf("deployment status = %s, want %s (cap+1 byte)", dep.Status, state.DeployFailed)
	}
}

// TestProcessOne_AppLayerUnderCapSucceeds is the negative-control twin
// of TestProcessOne_AppLayerOverCapFails. Hobby (512 MB cap) with a
// 1-byte tarball must hit the existing success path — rootfs stamped,
// snapshot_prime emitted. If this test regresses the cap logic is
// checking the wrong field (e.g. source_bytes instead of the produced
// layer size).
//
// Note: DeployLive is stamped by imaged (snapshot_prime → imaged handler),
// not by builderd. So we assert the builder-side success markers (rootfs
// stamped on the deployment row, cache populated, no error returned)
// rather than dep.Status — same pattern as TestProcessOne_VMSpawnSucceedsAndStamps.
func TestProcessOne_AppLayerUnderCapSucceeds(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json"})

	buildID, depID, _ := seedDeploymentWithPlan(t, store, src, "hobby")
	underCapPath := filepath.Join(t.TempDir(), "tiny.ext4")
	if err := os.WriteFile(underCapPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: underCapPath, ExitCode: 0, LogTailBytes: 1}}
	notif := &fakeNotifier{}
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	b := New(store, notif, fvm, c, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne (under cap): %v", err)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.RootfsPath == "" {
		t.Error("expected rootfs stamped on under-cap success")
	}
	// snapshot_boot must fire exactly once for the under-cap path —
	// the cap check runs *before* the boot notification, so a passing
	// test here confirms the order is right. (The split was added on
	// main: builderd emits NotifySnapshotBoot to imaged, which then
	// re-emits NotifySnapshotPrime for schedd. We only need to verify
	// builderd's contribution here.)
	bootCount := 0
	for _, call := range notif.calls {
		if call.channel == db.NotifySnapshotBoot {
			bootCount++
		}
	}
	if bootCount != 1 {
		t.Errorf("snapshot_boot count = %d, want 1", bootCount)
	}
}

// writeSparse creates a file of size bytes without allocating the full
// range — we only stat the file, not read it, so a sparse hole is fine
// and saves 600 MB of disk in the cap test above.
func writeSparse(t *testing.T, path string, size int64) error {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		return err
	}
	return nil
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

// scrapeMetrics renders the daemon's /metrics body via the OpsMetrics
// handler so build-metric assertions match the real exposition format.
func scrapeMetrics(t *testing.T, ops *wire.OpsMetrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	ops.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}

func TestProcessOne_EmitsBuildMetrics(t *testing.T) {
	// A fresh successful build increments ops_total{op="build",code="ok"}
	// and observes both build histograms exactly once (ADR-030).
	store := state.NewMemStore()
	srcTar := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, srcTar, []string{"package.json", "index.js"})
	buildID, _, _ := seedDeployment(t, store, srcTar)

	out := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(out, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: out, ExitCode: 0, LogTailBytes: 14}}
	ops := wire.NewOpsMetrics("builderd")
	b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	body := scrapeMetrics(t, ops)
	for _, want := range []string{
		`builderd_ops_total{code="ok",op="build"} 1`,
		`builderd_build_duration_seconds_count{outcome="ok"} 1`,
		`builderd_build_duration_seconds_count{outcome="cache_hit"} 0`,
		`builderd_build_duration_seconds_count{outcome="failed"} 0`,
		`builderd_build_queue_wait_seconds_count 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in metrics:\n%s", want, body)
		}
	}
}

func TestProcessOne_BuildMetricCodeByOutcome(t *testing.T) {
	// The ops_total code label must match the terminal outcome so the §12
	// build-success ratio (code!="user_error") is computed off real data,
	// AND the duration histogram's outcome label must match the funnel
	// (markSucceeded sets ok|cache_hit, markFailed sets failed). Coverage
	// for all four terminal classes lives here so a refactor that drops
	// either the code arg or the durationOutcome assignment is caught.
	srcTar := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "src.tar.gz")
		makeTarballWithName(t, p, []string{"package.json"})
		return p
	}
	outPath := func(t *testing.T) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "produced.ext4")
		if err := os.WriteFile(p, []byte("produced"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// detector that always errors — exercises the framework-detect → user_error path.
	// We point the deployment at a missing path so Detector.Detect returns an error
	// (FrameworkUnknown + "detect: open: …") and ProcessOne routes to
	// markFailed(FailureUserError, "framework detect: …").
	failingDetector := func() *Detector { return NewDetector() }

	t.Run("ok", func(t *testing.T) {
		store := state.NewMemStore()
		fvm := &fakeVM{out: BuildOutcome{OCIImage: outPath(t), ExitCode: 0, LogTailBytes: 9}}
		ops := wire.NewOpsMetrics("builderd")
		b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		_, _ = b.ProcessOne(context.Background(), mustSeed(t, store, srcTar(t)))
		assertCodes(t, ops, "ok", "ok")
	})

	t.Run("cache_hit", func(t *testing.T) {
		// Prime the cache so the second ProcessOne short-circuits via markSucceeded("cache_hit").
		store := state.NewMemStore()
		fvm := &fakeVM{out: BuildOutcome{OCIImage: outPath(t), ExitCode: 0, LogTailBytes: 9}}
		ops := wire.NewOpsMetrics("builderd")
		b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		// mustSeed creates an account+app+deployment+build. MemStore's
		// CreateAccount rejects duplicate emails, so vary the slug per
		// call (and indirectly the account id via the slug→app→dep
		// chain). The cache key is the SOURCE content hash — both
		// builds point at the same `src` so the second hits.
		src := srcTar(t)
		primeID := seedBuildForCachePrime(t, store, src)
		if _, err := b.ProcessOne(context.Background(), primeID); err != nil {
			t.Fatalf("first ProcessOne (cache prime): %v", err)
		}
		hitID := seedBuildForCachePrime(t, store, src)
		_, _ = b.ProcessOne(context.Background(), hitID)
		assertCodes(t, ops, "cache_hit", "cache_hit")
	})

	t.Run("user_error", func(t *testing.T) {
		// Failing detector → markFailed(FailureUserError, ...). §12 excludes this from success.
		// Point the deployment at a missing tarball so Detector.Detect errors out
		// ("detect: open: …") and ProcessOne routes to markFailed(FailureUserError).
		store := state.NewMemStore()
		fvm := &fakeVM{out: BuildOutcome{OCIImage: outPath(t), ExitCode: 0, LogTailBytes: 9}}
		ops := wire.NewOpsMetrics("builderd")
		b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), failingDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		_, _ = b.ProcessOne(context.Background(), mustSeed(t, store, filepath.Join(t.TempDir(), "does-not-exist.tar.gz")))
		assertCodes(t, ops, "user_error", "failed")
	})

	t.Run("infra", func(t *testing.T) {
		// nil vm driver → markFailed(FailureInfra, "vm driver not wired (metal only)").
		store := state.NewMemStore()
		ops := wire.NewOpsMetrics("builderd")
		b := New(store, &fakeNotifier{}, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		_, _ = b.ProcessOne(context.Background(), mustSeed(t, store, srcTar(t)))
		assertCodes(t, ops, "infra", "failed")
	})

	t.Run("oom", func(t *testing.T) {
		store := state.NewMemStore()
		fvm := &fakeVM{out: BuildOutcome{OCIImage: "/dev/null", ExitCode: 137, FailureClass: "FailureOOM"}}
		ops := wire.NewOpsMetrics("builderd")
		b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		_, _ = b.ProcessOne(context.Background(), mustSeed(t, store, srcTar(t)))
		assertCodes(t, ops, "oom", "failed")
	})

	t.Run("timeout", func(t *testing.T) {
		store := state.NewMemStore()
		fvm := &fakeVM{out: BuildOutcome{OCIImage: "/dev/null", ExitCode: 124, FailureClass: "FailureTimeout"}}
		ops := wire.NewOpsMetrics("builderd")
		b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		_, _ = b.ProcessOne(context.Background(), mustSeed(t, store, srcTar(t)))
		assertCodes(t, ops, "timeout", "failed")
	})
}

// mustSeed creates a fresh queued build row and returns its ID.
func mustSeed(t *testing.T, store state.Store, src string) string {
	t.Helper()
	id, _, _ := seedDeployment(t, store, src)
	return id
}

// seedBuildForCachePrime is a per-test-call helper for the cache_hit
// subtest. mustSeed routes through seedDeployment which hardcodes the
// email "u@example.com"; the MemStore rejects duplicate emails, so the
// cache_hit subtest (which seeds twice) needs unique accounts per
// call. Slug carries the test-scoped uniqueness; the rest of the chain
// falls out automatically.
func seedBuildForCachePrime(t *testing.T, store state.Store, src string) string {
	t.Helper()
	slug := fmt.Sprintf("prime-%d", time.Now().UnixNano())
	id, _, _ := seedDeploymentWithSlug(t, store, src, slug)
	return id
}

// assertCodes verifies the build counter's code label and the duration
// histogram's outcome label both surface in the /metrics body for the
// expected terminal class. ADR-030: counter is ops_total{op="build",code=…};
// duration histogram is build_duration_seconds{outcome=…}.
func assertCodes(t *testing.T, ops *wire.OpsMetrics, wantCode, wantOutcome string) {
	t.Helper()
	body := scrapeMetrics(t, ops)
	wantCounter := `builderd_ops_total{code="` + wantCode + `",op="build"} 1`
	if !strings.Contains(body, wantCounter) {
		t.Errorf("missing counter %q in:\n%s", wantCounter, body)
	}
	wantDur := `builderd_build_duration_seconds_count{outcome="` + wantOutcome + `"} 1`
	if !strings.Contains(body, wantDur) {
		t.Errorf("missing duration outcome %q in:\n%s", wantDur, body)
	}
}
