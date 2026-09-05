package builderd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"

	"github.com/google/uuid"
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
	out            BuildOutcome
	spawnErr       error
	waitErr        error
	waitHook       func()
	environment    BuildEnvironment
	environmentErr error
	spawnCalls     int
	waitCalls      int
	handle         BuildHandle
}

var testBuildEnvironment = BuildEnvironment{
	BuilderBaseIdentity: "sha256:test-builder-base",
	TargetPlatform:      "linux/amd64",
}

func (f *fakeVM) BuildEnvironment() (BuildEnvironment, error) {
	if f.environment == (BuildEnvironment{}) && f.environmentErr == nil {
		return testBuildEnvironment, nil
	}
	return f.environment, f.environmentErr
}

func testBuildCacheRecipe(sourceHash string, framework Framework, plan api.Plan, runtimeBaseRef string) BuildCacheRecipe {
	return BuildCacheRecipe{
		SourceSHA256:        sourceHash,
		Framework:           framework,
		Plan:                plan,
		RuntimeBaseRef:      runtimeBaseRef,
		BuilderBaseIdentity: testBuildEnvironment.BuilderBaseIdentity,
		TargetPlatform:      testBuildEnvironment.TargetPlatform,
	}
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
	if f.waitHook != nil {
		f.waitHook()
	}
	return f.out, f.waitErr
}

// Cancel satisfies the VM interface for ADR-124 cancel-LISTEN
// tests. The fake never errors (the unit tests don't exercise the
// build-cancel goroutine); a future test can swap this with a
// fault-injecting fake.
func (f *fakeVM) Cancel(_ context.Context, _ string) error {
	return nil
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
	if err := c.StoreBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal), layerPath, 18); err != nil {
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

func TestSnapshotBootPayloadIncludesBuilderNode(t *testing.T) {
	b := &Builderd{builderNodeID: "fsn-2.faas"}
	got := b.snapshotBootPayload("app-1", "dep-1")
	want := `{"app_id":"app-1","deployment_id":"dep-1","node_id":"fsn-2.faas"}`
	if got != want {
		t.Fatalf("snapshotBootPayload() = %q, want %q", got, want)
	}
}

func TestSnapshotBootPayloadOmitsEmptyBuilderNode(t *testing.T) {
	b := &Builderd{}
	got := b.snapshotBootPayload("app-1", "dep-1")
	want := `{"app_id":"app-1","deployment_id":"dep-1"}`
	if got != want {
		t.Fatalf("snapshotBootPayload() = %q, want %q", got, want)
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
	wantArtifactBytes := int64(len("produced layer"))
	if dep.RootfsBytes != wantArtifactBytes {
		t.Errorf("rootfs_bytes = %d, want artifact size %d", dep.RootfsBytes, wantArtifactBytes)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded", build.Status)
	}
	// Cache should have been populated.
	hash, _ := hashFile(srcTar)
	if _, ok := c.LookupBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal)); !ok {
		t.Error("expected cache populated after successful build")
	}
}

func TestProcessOne_CancelledCompletionDoesNotPublishArtifact(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	buildID, depID, _ := seedDeployment(t, store, src)

	layerPath := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(layerPath, []byte("should not publish"), 0o644); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{
		out: BuildOutcome{OCIImage: layerPath, ExitCode: 0},
		// Simulate a cancel racing with vmmd.Destroy returning a successful
		// outcome. The build row must win over the stale completion.
		waitHook: func() {
			if err := store.MarkBuildCancelled(context.Background(), buildID, depID, true, time.Now()); err != nil {
				t.Fatalf("MarkBuildCancelled: %v", err)
			}
		},
	}
	notif := &fakeNotifier{}
	cache := NewCache(t.TempDir())
	b := New(store, notif, fvm, cache, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	build, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatal(err)
	}
	if build.Status != state.BuildCancelled {
		t.Fatalf("build status = %s, want cancelled", build.Status)
	}
	dep, err := store.DeploymentByID(context.Background(), depID)
	if err != nil {
		t.Fatal(err)
	}
	if dep.RootfsPath != "" || dep.RootfsBytes != 0 {
		t.Fatalf("cancelled build published rootfs: path=%q bytes=%d", dep.RootfsPath, dep.RootfsBytes)
	}
	for _, call := range notif.calls {
		if call.channel == db.NotifySnapshotBoot {
			t.Fatal("cancelled build emitted snapshot_boot")
		}
	}
	hash, err := hashFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cache.LookupBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal)); ok {
		t.Fatal("cancelled build populated the cache")
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

// makeTarballWithName is a thin fixture helper for the build-roundtrip
// tests below. It builds a gzipped tarball at path whose top-level
// entries are the given names (empty content). Used to be a wrapper
// around the makeTarball helper in detect_test.go; that helper now
// lives in pkg/markers/detect_test.go and dropped with the deleted
// detect_test.go (issue #736 / ADR-088), so the helper is inlined
// here. Keep package-private — builderd's tests reach the markers
// package via DetectFromTarball etc., not via a fixture helper.
func makeTarballWithName(t *testing.T, path string, names []string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, n := range names {
		hdr := &tar.Header{Name: n, Mode: 0o644, Size: 0, Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
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
		// A tarball without framework markers makes Detector.Detect error out
		// ("detect: no package.json…") and ProcessOne routes to markFailed(FailureUserError).
		// The file must exist on disk: the source-spool-lag wait (issue: split-box
		// rsync) requeues missing sources instead of hard-failing them.
		store := state.NewMemStore()
		fvm := &fakeVM{out: BuildOutcome{OCIImage: outPath(t), ExitCode: 0, LogTailBytes: 9}}
		ops := wire.NewOpsMetrics("builderd")
		noMarkers := filepath.Join(t.TempDir(), "no-markers.tar.gz")
		makeTarballWithName(t, noMarkers, []string{"README.md"})
		b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), failingDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)
		_, _ = b.ProcessOne(context.Background(), mustSeed(t, store, noMarkers))
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

// TestMarkSucceededAndFailed_NilOpsAreSafe pins the OpsMetrics
// nil-safety contract (ADR-030) through Builderd's two metric
// funnels. The OpsMetrics methods each have an `if m == nil` guard,
// but the guards are only useful if Builderd actually goes through
// `b.ops` (a typed-nil pointer) without dereferencing first. A
// future refactor that swaps `b.ops.ObserveBuild*` for a direct
// field read would silently break tests that construct a Builderd
// without WithOpsMetrics — and ProcessOne's 25+ call sites would
// start panicking on the no-metrics unit-test path. This regression
// net stays exactly one assertion: build a Builderd with b.ops
// deliberately unset, drive markSucceeded + markFailed, observe zero
// panics. The store still flips BuildSucceeded / BuildFailed; only
// the Prometheus hand-off is a no-op.
func TestMarkSucceededAndFailed_NilOpsAreSafe(t *testing.T) {
	store := state.NewMemStore()
	srcTar := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, srcTar, []string{"package.json"})
	buildID, _, _ := seedDeployment(t, store, srcTar)

	b := New(store, &fakeNotifier{}, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if b.ops != nil {
		t.Fatalf("preconditions: ops must default to nil, got %#v", b.ops)
	}
	start := time.Now()

	// Issue #195 B1.4: UpdateBuildStatus has a CAS guard on
	// terminal writes — markSucceeded/markFailed only succeed if the
	// row is still 'running'. seedDeployment leaves the row as
	// 'queued'; flip it to 'running' first so the test exercises
	// the realistic in-flight build path.
	if err := store.UpdateBuildStatus(context.Background(), buildID, state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("seed running: %v", err)
	}

	// markSucceeded on a typed-nil *OpsMetrics: the ObserveBuildCount
	// + ObserveBuildDuration guards swallow the nil receiver; the
	// store mutation still flips BuildSucceeded.
	if err := store.UpdateBuildStatus(context.Background(), buildID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	b.observeSucceeded(context.Background(), buildID, "ok", start)
	got, err := store.BuildByID(context.Background(), buildID)
	if err != nil {
		t.Fatalf("BuildByID after markSucceeded: %v", err)
	}
	if got.Status != state.BuildSucceeded {
		t.Errorf("after markSucceeded: status=%q, want %q", got.Status, state.BuildSucceeded)
	}

	// markFailed on the same nil-ops Builderd for a different build.
	// Cross-check the failure funnel too — it observes under
	// code=<FailureClass> and outcome="failed" (different metrics
	// paths than markSucceeded), so a half-implemented guard wouldn't
	// catch it. seedDeployment reuses the same account slug so the
	// second row needs a unique slug (seedDeploymentWithSlug varies
	// the account email, dodging MemStore's duplicate-email guard).
	srcTar2 := filepath.Join(t.TempDir(), "src2.tar.gz")
	makeTarballWithName(t, srcTar2, []string{"package.json"})
	buildID2, _, _ := seedDeploymentWithSlug(t, store, srcTar2, "nil-ops-fail")
	// Same CAS guard: flip the second row to running first.
	if err := store.UpdateBuildStatus(context.Background(), buildID2, state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("seed running #2: %v", err)
	}
	claim2, _ := store.BuildByID(context.Background(), buildID2)
	b.markFailed(context.Background(), claim2, state.FailureInfra, "nil-ops regression: infra failure path", start)
	got2, err := store.BuildByID(context.Background(), buildID2)
	if err != nil {
		t.Fatalf("BuildByID after markFailed: %v", err)
	}
	if got2.Status != state.BuildFailed {
		t.Errorf("after markFailed: status=%q, want %q", got2.Status, state.BuildFailed)
	}
	if got2.FailureClass != state.FailureInfra {
		t.Errorf("after markFailed: failure_class=%q, want %q", got2.FailureClass, state.FailureInfra)
	}
}

// ----------------------------------------------------------------------
// PR-B: ProcessNext (durable worker surface) + ErrNoSlot requeue
// ----------------------------------------------------------------------

// TestProcessNext_EmptyQueueReturnsErrNotFound pins the durably-empty
// idle path: ClaimNextQueuedBuild returns ErrNotFound, ProcessNext
// surfaces it without touching the row, and the build store stays
// empty. workerLoop in cmd/builderd translates this into a no-op tick.
func TestProcessNext_EmptyQueueReturnsErrNotFound(t *testing.T) {
	store := state.NewMemStore()
	b := New(store, &fakeNotifier{}, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	_, err := b.ProcessNext(context.Background())
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ProcessNext empty queue: got %v, want state.ErrNotFound", err)
	}
}

func TestMaterializeSourceFromStorage(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	buildID := "550e8400-e29b-41d4-a716-446655440000"
	want := []byte("source-from-shared-storage")
	if err := be.Put(context.Background(), "sources/"+buildID+".tar.gz", bytes.NewReader(want)); err != nil {
		t.Fatalf("Put source: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "builds", buildID+".tar.gz")
	b := New(state.NewMemStore(), &fakeNotifier{}, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithSourceStorage(be)
	if err := b.materializeSource(context.Background(), buildID, dst); err != nil {
		t.Fatalf("materializeSource: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("materialized source = %q, want %q", got, want)
	}
}

type eventuallyAvailableStorage struct {
	storage.StorageBackend
	missing int
	calls   int
}

func (s *eventuallyAvailableStorage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	s.calls++
	if s.calls <= s.missing {
		return nil, storage.ErrNotFound
	}
	return s.StorageBackend.Get(ctx, key)
}

func TestWaitForSourceRetriesRemoteNotFound(t *testing.T) {
	be, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	buildID := "550e8400-e29b-41d4-a716-446655440000"
	want := []byte("source-after-registry-visibility-delay")
	if err := be.Put(context.Background(), "sources/"+buildID+".tar.gz", bytes.NewReader(want)); err != nil {
		t.Fatalf("Put source: %v", err)
	}
	delayed := &eventuallyAvailableStorage{StorageBackend: be, missing: 2}
	dst := filepath.Join(t.TempDir(), "builds", buildID+".tar.gz")
	b := New(state.NewMemStore(), &fakeNotifier{}, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithSourceStorage(delayed)

	// ProcessOne performs this first lookup before entering the bounded
	// local/remote wait loop. Simulate that initial registry miss here.
	if err := b.materializeSource(context.Background(), buildID, dst); err != nil {
		t.Fatalf("initial materializeSource: %v", err)
	}
	if err := b.waitForSource(context.Background(), buildID, dst, 2*time.Second); err != nil {
		t.Fatalf("waitForSource: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("materialized source = %q, want %q", got, want)
	}
	if delayed.calls < 3 {
		t.Fatalf("source backend calls = %d, want at least 3", delayed.calls)
	}
}

// TestProcessNext_ClaimsQueuedRowAndRuns is the happy path: a row sits
// in queued, ProcessNext CAS-claims it via SKIP LOCKED-equivalent logic
// and runs the build to success. Verifies the new surface shares the
// pipeline with ProcessOne 1:1 — same VM spawn count, same terminal
// status, same cache stamp.
func TestProcessNext_ClaimsQueuedRowAndRuns(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, depID, _ := seedDeployment(t, store, src)

	out := filepath.Join(t.TempDir(), "produced.ext4")
	if err := os.WriteFile(out, []byte("produced layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	fvm := &fakeVM{out: BuildOutcome{OCIImage: out, ExitCode: 0, LogTailBytes: 14}}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessNext(context.Background()); err != nil {
		t.Fatalf("ProcessNext: %v", err)
	}
	if fvm.spawnCalls != 1 {
		t.Errorf("VM spawn was called %d times, want 1", fvm.spawnCalls)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.RootfsPath == "" {
		t.Error("rootfs_path not stamped on deployment")
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded", build.Status)
	}
}

// TestProcessNext_NoSlotRequeuesRowAndReturnsErrNoSlot pins PR-B §B.5:
// DecideSlot returning !Allowed must RequeueBuild (NOT markFailed) so
// the build row stays in queued for the next worker tick. We swap in a
// deny-all decider via WithSlotDecider so we don't have to thread a
// ResidencyProbe through. The deployment row stays "building" — no
// false DeployFailed flip.
func TestProcessNext_NoSlotRequeuesRowAndReturnsErrNoSlot(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, depID, _ := seedDeployment(t, store, src)

	fvm := &fakeVM{} // would panic if called — proves no spawn.
	b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSlotDecider(func(_ ResidencyProbe, _ int) SlotDecision {
			return SlotDecision{Allowed: false, Label: "denied", Reason: "test-injected denial"}
		})

	_, err := b.ProcessNext(context.Background())
	if !errors.Is(err, ErrNoSlot) {
		t.Fatalf("ProcessNext no-slot: got %v, want ErrNoSlot", err)
	}
	if fvm.spawnCalls != 0 {
		t.Errorf("VM spawn was called %d times, want 0 (denied before spawn)", fvm.spawnCalls)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildQueued {
		t.Errorf("build status = %s, want queued (requeued)", build.Status)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status == state.DeployFailed {
		t.Errorf("deployment flipped to failed on no-slot; want unchanged (was %s)", dep.Status)
	}
}

// TestProcessNext_NoSlotPreservesEnqueuedAt pins the FIFO-survival
// contract: after a no-slot requeue the build's enqueued_at is
// unchanged so the next ProcessNext claim doesn't reshuffle the queue.
// Without this, a wake-surge holding the slot would let a build
// submitted later jump the line.
func TestProcessNext_NoSlotPreservesEnqueuedAt(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, _, _ := seedDeployment(t, store, src)
	originalEnq, _ := store.BuildByID(context.Background(), buildID)
	if originalEnq.EnqueuedAt.IsZero() {
		t.Fatal("seedDeployment: enqueued_at is zero; can't pin FIFO")
	}

	b := New(store, &fakeNotifier{}, &fakeVM{}, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSlotDecider(func(_ ResidencyProbe, _ int) SlotDecision {
			return SlotDecision{Allowed: false, Label: "denied", Reason: "test-injected denial"}
		})

	// Tick once: claim → deny → requeue.
	if _, err := b.ProcessNext(context.Background()); !errors.Is(err, ErrNoSlot) {
		t.Fatalf("tick 1: got %v, want ErrNoSlot", err)
	}
	afterTick, _ := store.BuildByID(context.Background(), buildID)
	if !afterTick.EnqueuedAt.Equal(originalEnq.EnqueuedAt) {
		t.Errorf("enqueued_at shifted: was %s, now %s", originalEnq.EnqueuedAt, afterTick.EnqueuedAt)
	}
	if afterTick.StartedAt != (time.Time{}) {
		t.Errorf("started_at not cleared on requeue: %s", afterTick.StartedAt)
	}
}

// TestProcessOne_NoSlotDoesNotMarkFailed pins the parity between the
// LISTEN surface (ProcessOne) and the poll surface (ProcessNext) for
// the no-slot path. PR-B unified the requeue inside
// processClaimedBuild so a missed-notify redeploy cannot end up
// marked failed when slots open up later.
func TestProcessOne_NoSlotDoesNotMarkFailed(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, depID, _ := seedDeployment(t, store, src)

	fvm := &fakeVM{}
	b := New(store, &fakeNotifier{}, fvm, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithSlotDecider(func(_ ResidencyProbe, _ int) SlotDecision {
			return SlotDecision{Allowed: false, Label: "denied", Reason: "test-injected denial"}
		})

	// ProcessOne accepts a buildID; pass through the same code path.
	if _, err := b.ProcessOne(context.Background(), buildID); !errors.Is(err, ErrNoSlot) {
		t.Fatalf("ProcessOne no-slot: got %v, want ErrNoSlot", err)
	}
	build, _ := store.BuildByID(context.Background(), buildID)
	if build.Status != state.BuildQueued {
		t.Errorf("build status = %s, want queued", build.Status)
	}
	dep, _ := store.DeploymentByID(context.Background(), depID)
	if dep.Status == state.DeployFailed {
		t.Errorf("deployment flipped to failed on no-slot; want unchanged")
	}
}

// --- B2.2 (issue #196): per-account claim fairness ---

// TestProcessNext_FairnessWindow_ZeroDisablesFilter verifies that
// FairnessWindow=0 makes ProcessNext call the legacy FIFO claim
// (ClaimNextQueuedBuild), not the fairness variant — the regression
// gate for "operators can disable the fairness filter". A bug here
// would surprise operators whose workloads behave fine without the
// filter (single-customer, no contention).
//
// Implementation note: ProcessNext runs the full pipeline. To avoid
// the VM spawn path entirely we pre-populate the cache so the
// processClaimedBuild path short-circuits at the cache hit (no VM
// spawn, no BuildDriveDir required).
func TestProcessNext_FairnessWindow_ZeroDisablesFilter(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	buildID, _, _ := seedDeploymentWithSlug(t, store, src, fmt.Sprintf("fifo-zero-%s", uuid.NewString()[:6]))

	// Pre-record the queue's account as recent; with FairnessWindow=0
	// (DISABLED) the FIFO claim still picks it. With FairnessWindow=30s
	// the next test verifies the opposite.
	b, _ := store.BuildByID(context.Background(), buildID)
	dep, _ := store.DeploymentByID(context.Background(), b.DeploymentID)
	app, _ := store.AppByID(context.Background(), dep.AppID)
	if err := store.RecordRecentBuildClaim(context.Background(), app.AccountID, uuid.NewString()); err != nil {
		t.Fatalf("seed skip: %v", err)
	}

	// Pre-cache the source so the pipeline short-circuits at the
	// cache-hit branch (which writes deployment rootfs and skips VM spawn).
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	srcCopy := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(srcCopy, []byte("pre-cached layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(src)
	if err := c.StoreBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal), srcCopy, 17); err != nil {
		t.Fatal(err)
	}

	ops := wire.NewOpsMetrics("builderd")
	bld := New(store, &fakeNotifier{}, &fakeVM{}, c, NewDetector(), nil,
		Config{BuildTimeoutSeconds: 1, FairnessWindow: 0}, // DISABLED
		slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)

	if _, err := bld.ProcessNext(context.Background()); err != nil && !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ProcessNext: %v", err)
	}
	// Build should now be succeeded (cache hit short-circuits to terminal).
	post, _ := store.BuildByID(context.Background(), buildID)
	if post.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded", post.Status)
	}
}

// TestProcessNext_FairnessWindow_PreferQuietAccount is the wired-up
// gate for B2.2: after marking A as recent, ProcessNext must skip
// A's queued builds and pick B or C. The cache-hit path lets us run
// the full pipeline without spinning up a VM.
func TestProcessNext_FairnessWindow_PreferQuietAccount(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	// Three accounts × one build each. The three builds share a
	// source-hash (all call seedDeploymentWithSlug with the same
	// source) so a single Cache.Store primes all three with one
	// write.
	type seedInfo struct {
		accountID string
		buildID   string
	}
	var seeded []seedInfo
	for i := 0; i < 3; i++ {
		buildID, _, _ := seedDeploymentWithSlug(t, store, src, fmt.Sprintf("fair-%d-%s", i, uuid.NewString()[:6]))
		b, _ := store.BuildByID(context.Background(), buildID)
		dep, _ := store.DeploymentByID(context.Background(), b.DeploymentID)
		app, _ := store.AppByID(context.Background(), dep.AppID)
		seeded = append(seeded, seedInfo{accountID: app.AccountID, buildID: buildID})
	}

	if err := store.RecordRecentBuildClaim(context.Background(), seeded[0].accountID, uuid.NewString()); err != nil {
		t.Fatalf("seed skip: %v", err)
	}

	// Pre-cache the source so each ProcessNext short-circuits at the
	// cache hit branch.
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	srcCopy := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(srcCopy, []byte("pre-cached layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(src)
	if err := c.StoreBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal), srcCopy, 17); err != nil {
		t.Fatal(err)
	}

	ops := wire.NewOpsMetrics("builderd")
	bld := New(store, &fakeNotifier{}, &fakeVM{}, c, NewDetector(), nil,
		Config{BuildTimeoutSeconds: 1, FairnessWindow: 30 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)

	// Two ProcessNext calls: each must pick a non-A build (cache hit).
	for i := 0; i < 2; i++ {
		if _, err := bld.ProcessNext(context.Background()); err != nil {
			t.Fatalf("ProcessNext[%d]: %v", i, err)
		}
	}
	// A's build must still be queued.
	post, _ := store.BuildByID(context.Background(), seeded[0].buildID)
	if post.Status != state.BuildQueued {
		t.Errorf("A's build status = %s, want queued (fairness should have skipped A's queued row)", post.Status)
	}
}

// failingRecordStore embeds *state.MemStore and overrides only
// RecordRecentBuildClaim to always error. It's the seam for the
// "record is best-effort" invariant: a transient DB outage on the
// recent_build_claims insert must NOT fail the build.
type failingRecordStore struct {
	*state.MemStore
}

func (f *failingRecordStore) RecordRecentBuildClaim(_ context.Context, _, _ string) error {
	return errors.New("simulated record failure")
}

// TestProcessNext_RecordRecentBuildClaim_FailureDoesNotFailBuild pins
// the B2.2 critical-invariant #2 (record is best-effort): when
// RecordRecentBuildClaim errors after a successful claim, the build
// itself still reaches BuildSucceeded. processClaimedBuild's
// warn-log-on-error path is the only correct behavior — the claim
// must NOT be rolled back, the deployment must NOT be flipped to
// failed, and the cache-hit short-circuit must still resolve to a
// terminal-success state.
func TestProcessNext_RecordRecentBuildClaim_FailureDoesNotFailBuild(t *testing.T) {
	store := &failingRecordStore{MemStore: state.NewMemStore()}
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	buildID, _, _ := seedDeploymentWithSlug(t, store.MemStore, src, fmt.Sprintf("record-fail-%s", uuid.NewString()[:6]))

	// Pre-cache the source so the pipeline short-circuits at the
	// cache-hit branch (which writes deployment rootfs and skips VM spawn).
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	srcCopy := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(srcCopy, []byte("pre-cached layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(src)
	if err := c.StoreBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal), srcCopy, 17); err != nil {
		t.Fatal(err)
	}

	ops := wire.NewOpsMetrics("builderd")
	bld := New(store, &fakeNotifier{}, &fakeVM{}, c, NewDetector(), nil,
		Config{BuildTimeoutSeconds: 1, FairnessWindow: 30 * time.Second},
		slog.New(slog.NewTextHandler(io.Discard, nil))).WithOpsMetrics(ops)

	if _, err := bld.ProcessNext(context.Background()); err != nil {
		t.Fatalf("ProcessNext must succeed even when RecordRecentBuildClaim fails: %v", err)
	}
	// Build must reach BuildSucceeded — the record failure must NOT
	// have rolled back the claim or flipped the deployment to failed.
	post, _ := store.BuildByID(context.Background(), buildID)
	if post.Status != state.BuildSucceeded {
		t.Errorf("build status = %s, want succeeded (record failure must not poison the build)", post.Status)
	}
	dep, _ := store.DeploymentByID(context.Background(), post.DeploymentID)
	if dep.Status == state.DeployFailed {
		t.Errorf("deployment flipped to failed on record error; want unchanged")
	}
}

// TestProcessOne_CacheHitPersistsProvenance — ADR-038 / Tier 3
// / issue #197 B3.1: the cache-hit markSucceeded path must land
// a build_provenance row. Catches the regression where the
// populator is only wired at the fresh-build markSucceeded site
// (the second site's symPole in the cache-hit branch closes).
func TestProcessOne_CacheHitPersistsProvenance(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, _, _ := seedDeployment(t, store, src)

	// Pre-populate the cache so the lookup hits.
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	layerPath := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(layerPath, []byte("pre-cached layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(src)
	if err := c.StoreBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal), layerPath, 18); err != nil {
		t.Fatal(err)
	}

	fvm := &fakeVM{} // proves the spawn was skipped.
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, c, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	// The populator must have landed a row keyed by build_id.
	prov, err := store.BuildProvenanceByBuildID(context.Background(), buildID)
	if err != nil {
		t.Fatalf("BuildProvenanceByBuildID: %v (populator MUST land on the cache-hit path)", err)
	}
	if prov.SourceSHA256 != hash {
		t.Errorf("SourceSHA256 = %q, want %q", prov.SourceSHA256, hash)
	}
	if prov.Plan != string(api.PlanPro) {
		t.Errorf("Plan = %q, want %q", prov.Plan, api.PlanPro)
	}
}

// TestProcessOne_FreshBuildPersistsProvenance — the OTHER
// markSucceeded site (the spawn path) must also populate the
// row. Coverage is lighter here because the spawn path is
// gated behind fvm.spawnCalls > 0; we just check the row lands.
// The deeper source_url / commit_sha stamping is verified in
// TestProcessOne_ProvenanceCopiesDeploymentSourceFields below.
func TestProcessOne_FreshBuildPersistsProvenance(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})

	buildID, _, _ := seedDeployment(t, store, src)

	// No cache pre-population → fresh-build branch.
	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	// Layer must exist for cache.Store (AppLayerMaxMB stat check)
	// in the post-build bookkeeping.
	layerPath := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(layerPath, []byte("tiny layer content"), 0o644); err != nil {
		t.Fatal(err)
	}

	fvm := &fakeVM{
		// fakeVM.WaitForCompletion returns f.out; pre-set it so the
		// spawn path produces the canned layer path.
		out: BuildOutcome{OCIImage: layerPath, ExitCode: 0, LogTailBytes: 0},
	}
	notif := &fakeNotifier{}
	b := New(store, notif, fvm, c, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	prov, err := store.BuildProvenanceByBuildID(context.Background(), buildID)
	if err != nil {
		t.Fatalf("BuildProvenanceByBuildID: %v (fresh-build path must populate)", err)
	}
	if prov.SourceSHA256 == "" {
		t.Errorf("SourceSHA256 = empty, want non-empty (hashFile at line ~318)")
	}
	if prov.Plan != string(api.PlanPro) {
		t.Errorf("Plan = %q, want %q", prov.Plan, api.PlanPro)
	}
}

// TestProcessOne_ProvenanceCopiesDeploymentSourceFields —
// guards the ADR-038 "what ran?" propagation. The provenance
// source_url + commit_sha are copied from the deployment row
// (Phase 1 columns from migration 00047); a regression that
// drops the copy is silent without this test.
func TestProcessOne_ProvenanceCopiesDeploymentSourceFields(t *testing.T) {
	store := state.NewMemStore()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, src, []string{"package.json", "index.js"})
	buildID, depID, _ := seedDeployment(t, store, src)

	const wantURL = "https://github.com/acme/app@main"
	const wantSHA = "0123456789abcdef0123456789abcdef01234567"
	if err := store.SetDeploymentSourceURL(context.Background(), depID, wantURL, wantSHA); err != nil {
		t.Fatalf("SetDeploymentSourceURL: %v", err)
	}

	cacheRoot := t.TempDir()
	c := NewCache(cacheRoot)
	layerPath := filepath.Join(t.TempDir(), "layer.ext4")
	if err := os.WriteFile(layerPath, []byte("pre-cached layer"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := hashFile(src)
	if err := c.StoreBuild(testBuildCacheRecipe(hash, FrameworkNode, api.PlanPro, imaged.BaseRefMinimal), layerPath, 18); err != nil {
		t.Fatal(err)
	}

	b := New(store, &fakeNotifier{}, &fakeVM{}, c, NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := b.ProcessOne(context.Background(), buildID); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	prov, err := store.BuildProvenanceByBuildID(context.Background(), buildID)
	if err != nil {
		t.Fatalf("BuildProvenanceByBuildID: %v", err)
	}
	if prov.SourceURL != wantURL {
		t.Errorf("SourceURL = %q, want %q (populator must copy from deployments.source_url)", prov.SourceURL, wantURL)
	}
	if prov.CommitSHA != wantSHA {
		t.Errorf("CommitSHA = %q, want %q (populator must copy from deployments.commit_sha)", prov.CommitSHA, wantSHA)
	}
}

// TestMarkSucceededAndFailed_EmitBuildEvents pins the
// wake.build_succeeded / wake.build_failed emit (issue #517 /
// PR-C / ADR-064). The two metrics funnels MUST write the
// typed events row under the right deployment + carry the
// image_digest + duration payload so the customer-facing
// timeline endpoint (commit 8) can render the build phase
// without a hand-rolled SELECT data->>'deployment_id' FROM
// events.
//
// The test seeds two builds: a success path (markSucceeded)
// and a failure path (markFailed). Both run with b.events
// wired; the events table is read back via store.ListEvents
// (the same query the production apid wake-timeline endpoint
// uses).
func TestMarkSucceededAndFailed_EmitBuildEvents(t *testing.T) {
	store := state.NewMemStore()
	srcTar := filepath.Join(t.TempDir(), "src.tar.gz")
	makeTarballWithName(t, srcTar, []string{"package.json"})
	buildID, depID, _ := seedDeployment(t, store, srcTar)
	srcTar2 := filepath.Join(t.TempDir(), "src2.tar.gz")
	makeTarballWithName(t, srcTar2, []string{"package.json"})
	buildID2, depID2, _ := seedDeploymentWithSlug(t, store, srcTar2, "build-evt-fail")

	eventsPlatform := events.NewPlatform("builderd", store, slog.New(slog.NewTextHandler(io.Discard, nil)), wire.NewOpsMetrics("builderd-test"), nil)

	b := New(store, &fakeNotifier{}, nil, NewCache(t.TempDir()), NewDetector(), nil, Config{}, slog.New(slog.NewTextHandler(io.Discard, nil))).
		WithEvents(eventsPlatform)

	// Flip both rows to running so the markX CAS guard passes.
	if err := store.UpdateBuildStatus(context.Background(), buildID, state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("seed running: %v", err)
	}
	if err := store.UpdateBuildStatus(context.Background(), buildID2, state.BuildRunning, "", true, false); err != nil {
		t.Fatalf("seed running #2: %v", err)
	}

	// Success path.
	if err := store.UpdateBuildStatus(context.Background(), buildID, state.BuildSucceeded, "", false, true); err != nil {
		t.Fatal(err)
	}
	b.observeSucceeded(context.Background(), buildID, "ok", time.Now().Add(-50*time.Millisecond))
	// Failure path — using FailureInfra (covers the most common
	// non-user-error path; FailureUserError is a separate funnel
	// in metrics but the typed event uses the same string).
	claim2, _ := store.BuildByID(context.Background(), buildID2)
	b.markFailed(context.Background(), claim2, state.FailureInfra, "synthetic infra", time.Now().Add(-30*time.Millisecond))

	// Read the events table back. Two rows expected:
	// wake.build_succeeded (buildID) and wake.build_failed
	// (buildID2). Subject is the build events' Subject
	// (nil — these are system-level events not pinned to a
	// single app subject) so the empty-subject filter
	// matches.
	rows, err := store.ListEvents(context.Background(), "", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	gotByKind := map[string]int{}
	gotByDep := map[string]string{}
	for _, row := range rows {
		var payload map[string]any
		_ = json.Unmarshal(row.Data, &payload)
		gotByKind[row.Kind]++
		if dep, ok := payload["deployment_id"].(string); ok {
			gotByDep[row.Kind] = dep
		}
	}
	if gotByKind["wake.build_succeeded"] != 1 {
		t.Errorf("wake.build_succeeded count = %d, want 1 (gotByKind=%v)", gotByKind["wake.build_succeeded"], gotByKind)
	}
	if gotByKind["wake.build_failed"] != 1 {
		t.Errorf("wake.build_failed count = %d, want 1 (gotByKind=%v)", gotByKind["wake.build_failed"], gotByKind)
	}
	if gotByDep["wake.build_succeeded"] != depID {
		t.Errorf("wake.build_succeeded deployment_id = %q, want %q", gotByDep["wake.build_succeeded"], depID)
	}
	if gotByDep["wake.build_failed"] != depID2 {
		t.Errorf("wake.build_failed deployment_id = %q, want %q", gotByDep["wake.build_failed"], depID2)
	}
}
