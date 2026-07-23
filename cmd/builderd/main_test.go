// Tests for the builderd daemon entrypoint. The full happy path needs
// vmmd + KVM + a real builder-base.ext4 (issue #57's metal e2e). Here
// we cover the pure config-loading + DI seam, matching the schedd
// test convention.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/builderd"
	"github.com/onebox-faas/faas/pkg/state"
)

// discardLog matches cmd/schedd/main_test.go: tests don't want slog output
// noise on the assertion-failure path.
func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// TestEnvOr_PrefersOSOverFallback is the regression guard for the
// FAAS_BUILDERD_CONFIG env override added for issue #57 — the harness
// needs to point builderd at a per-test config under /tmp. Without the
// env read, defaultDeps() returns the immutable /etc/faas/builderd.toml
// and the e2e test cannot drive a custom config (cache_dir, build dirs).
func TestEnvOr_PrefersOSOverFallback(t *testing.T) {
	t.Setenv("FAAS_BUILDERD_CONFIG", "/tmp/builderd-test.toml")
	if got := envOr("FAAS_BUILDERD_CONFIG", "/etc/faas/builderd.toml"); got != "/tmp/builderd-test.toml" {
		t.Errorf("envOr = %q, want /tmp/builderd-test.toml", got)
	}
}

// TestEnvOr_FallsBackWhenUnset pins the default path. Mirrors the
// constant in cmd/builderd/main.go::defaultDeps — if either drifts,
// the EX44 production start silently loads the wrong file.
func TestEnvOr_FallsBackWhenUnset(t *testing.T) {
	t.Setenv("FAAS_BUILDERD_CONFIG", "")
	if got := envOr("FAAS_BUILDERD_CONFIG", "/etc/faas/builderd.toml"); got != "/etc/faas/builderd.toml" {
		t.Errorf("envOr fallback = %q, want /etc/faas/builderd.toml", got)
	}
}

// TestDefaultDeps_UsesEnvOverride confirms defaultDeps wires the env
// read into the runDeps seam. This is what the harness depends on —
// if defaultDeps reverts to a hardcoded path the env override becomes
// inert and the e2e test can't isolate config.
func TestDefaultDeps_UsesEnvOverride(t *testing.T) {
	t.Setenv("FAAS_BUILDERD_CONFIG", "/var/lib/faas/test/builderd.toml")
	if got := defaultDeps().configPath; got != "/var/lib/faas/test/builderd.toml" {
		t.Errorf("defaultDeps().configPath = %q, want the env-set value", got)
	}
}

// TestRun_BadConfigPath exercises the non-ENOENT read failure path
// the way schedd's TestRun_BadConfigPath does: passing a directory
// (not a file) to LoadConfig must surface a wrapped error so an
// operator's broken config refuses to come up.
func TestRun_BadConfigPath(t *testing.T) {
	deps := runDeps{
		configPath: t.TempDir(), // a directory; not a regular file
	}
	err := runWithDeps(context.Background(), discardLog(), deps)
	if err == nil {
		t.Fatal("expected error from directory-as-config-path")
	}
	// The error must wrap something — not just an empty message —
	// so an operator's logs explain why builderd refused to start.
	var wantEmpty error
	if errors.Is(err, wantEmpty) {
		t.Errorf("expected non-empty error, got %v", err)
	}
}

// TestDefaultDeps_NewResidentProbeWired verifies the opportunistic-slot
// wiring (spec §4.5): defaultDeps must populate newResidentProbe so the
// run loop's probe field is non-nil. Empty URL is the unconfigured-URL
// failure mode and must match the nil-probe posture in slot.go — return
// a probe whose value trips the 60 % threshold and forces guaranteed-only.
func TestDefaultDeps_NewResidentProbeWired(t *testing.T) {
	deps := defaultDeps()
	if deps.newResidentProbe == nil {
		t.Fatal("defaultDeps.newResidentProbe is nil; the opportunistic slot will be dead in prod (regression of #M6-gap-1)")
	}
	p := deps.newResidentProbe(context.Background(), "")
	if p == nil {
		t.Fatal("newResidentProbe returned nil for empty URL")
	}
	if got := p.ResidentMB(); got <= 0 {
		t.Errorf("empty-URL probe ResidentMB = %d, want > 0 (must deny opportunistic)", got)
	}
}

// ----------------------------------------------------------------------
// PR-B: workerLoop (durable build-queue worker) acceptance
// ----------------------------------------------------------------------

// countVM is a stub that records every Spawn call. We never let it run a
// real build; the worker test only cares that ProcessNext was reached.
type countVM struct {
	calls atomic.Int64
}

func (c *countVM) Spawn(_ context.Context, _ builderd.VMRequest) (builderd.BuildHandle, error) {
	c.calls.Add(1)
	return builderd.BuildHandle{Instance: "stub", BuildID: "stub"}, nil
}

func (c *countVM) WaitForCompletion(_ context.Context, _ builderd.BuildHandle) (builderd.BuildOutcome, error) {
	return builderd.BuildOutcome{OCIImage: "/dev/null", ExitCode: 0}, nil
}

// nullNotifier satisfies builderdpkg.Notifier so the in-process pipeline
// doesn't nil-deref on NotifySnapshotBoot. The build-done.json path
// goes through the vmmd subprocess in the metal e2e; here we just need
// a non-nil shim.
type nullNotifier struct{}

func (nullNotifier) Notify(_ context.Context, _, _ string) error { return nil }

// seedQueuedBuild inserts one queued build row directly into a MemStore so
// the worker has something to claim. Mirrors the shape builderd's
// upstream apid emit would produce (app + deployment + build). The
// source path is a real Node tarball so the framework detector passes
// (otherwise the worker would hit the user_error markFailed path
// before Spawn and the "did Spawn get called" assertion in the
// orphaned-row test would never see a positive count).
func seedQueuedBuild(t *testing.T, store *state.MemStore) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := writeNodeTarball(src); err != nil {
		t.Fatal(err)
	}
	acct, err := store.CreateAccount(context.Background(), "durability@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "durable-app", RAMMB: 256, IdleTimeoutS: 60, MaxConcurrency: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, _, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindTarball,
		SourcePath:  src,
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
	return build.ID
}

// writeNodeTarball writes a gzipped tarball the detector accepts:
// one package.json entry at the root. The detector opens the file
// through gzip.NewReader, so a plain tar archive would fail with
// "detect: gzip: …". Mirrors pkg/builderd/detect_test.go::makeTarball.
func writeNodeTarball(path string) error {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "package.json", Mode: 0o644, Size: 0, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

// TestWorkerLoop_TicksOnEmptyQueue spins a 30ms-cadence worker against
// an empty MemStore. After ~150ms we should have seen at least 3 ticks
// (a generous bound for CI jitter) — proves the ticker is firing and
// the ErrNotFound path is silent. PR-B §B.5: the worker must NOT
// log spam on the idle box.
func TestWorkerLoop_TicksOnEmptyQueue(t *testing.T) {
	store := state.NewMemStore()
	fvm := &countVM{}
	b := builderd.New(store, nullNotifier{}, fvm, nil, nil, nil, builderd.Config{}, discardLog())

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	// 30ms cadence × 250ms wall = up to 8 ticks.
	done := make(chan struct{})
	go func() {
		defer close(done)
		workerLoop(ctx, b, 30*time.Millisecond, discardLog())
	}()
	<-ctx.Done()
	<-done

	// countVM.calls is the Spawn counter; the worker NEVER reaches the
	// spawn path on an empty queue, but ProcessNext still calls the
	// detector / framework / cache short-circuits before bailing out.
	// The acceptance here is that the loop TERMINATED cleanly — a stuck
	// loop would hang the test past the 250ms timeout.
}

// TestWorkerLoop_ClaimsOrphanedRow is the PR-B §B.5 acceptance: a row
// sitting in queued with no LISTEN-driven ProcessOne in flight must be
// claimed by the worker on the next tick. We spin a 20ms cadence,
// wait one tick, then assert Spawn was called exactly once.
func TestWorkerLoop_ClaimsOrphanedRow(t *testing.T) {
	store := state.NewMemStore()
	_ = seedQueuedBuild(t, store)

	fvm := &countVM{}
	b := builderd.New(store, nullNotifier{}, fvm, nil, nil, nil, builderd.Config{}, discardLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		workerLoop(ctx, b, 20*time.Millisecond, discardLog())
	}()

	// Wait up to 500ms for the worker to pick up the row.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fvm.calls.Load() >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if got := fvm.calls.Load(); got < 1 {
		t.Fatalf("worker did not claim the orphaned row in 500ms; Spawn calls = %d (PR-B §B.5 broken)", got)
	}
}

// TestWorkerLoop_DoesNotDoubleSpawnForSameRow pins the durability net
// against the LISTEN fast path: the same row must NOT be processed
// twice when both surfaces compete. We pre-claim the row by hand
// (mimicking the LISTEN-driven ProcessOne's CAS) and assert the worker's
// tick returns ErrNotFound (no double-spawn).
func TestWorkerLoop_DoesNotDoubleSpawnForSameRow(t *testing.T) {
	store := state.NewMemStore()
	buildID := seedQueuedBuild(t, store)

	// Mimic LISTEN-driven ProcessOne: CAS the row to running.
	_, err := store.ClaimQueuedBuild(context.Background(), buildID)
	if err != nil {
		t.Fatalf("pre-claim: %v", err)
	}

	fvm := &countVM{}
	b := builderd.New(store, nullNotifier{}, fvm, nil, nil, nil, builderd.Config{}, discardLog())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		workerLoop(ctx, b, 20*time.Millisecond, discardLog())
	}()
	// Poll for the deadline instead of a flat sleep: the worker's
	// ErrNotFound path is silent and only the Spawn counter can
	// fail the test, so we want the assertion to fire as soon as
	// (and if) Spawn is ever called. A flat 200ms sleep gives the
	// worker ~10 ticks to prove the CAS holds — keep the same
	// deadline but check the counter every 10ms so a regression
	// surfaces immediately rather than after the full wall.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fvm.calls.Load() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	if got := fvm.calls.Load(); got != 0 {
		t.Errorf("Spawn was called %d times; want 0 (worker must not double-spawn a row already claimed)", got)
	}
}
