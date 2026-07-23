package imaged

// Focused tests for F2 (FC-version startup sweep), F3 (function-runner
// wiring), and F4 (snapshot_boot canonical handler). The base happy-path
// + redelivery coverage for F4 lives in handler_test.go (TestHandleBuildQueued).

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- F2: FC-version startup sweep ------------------------------------------

func TestFCSweep_MarksStaleOnlyOlderVersion(t *testing.T) {
	store := state.NewMemStore()
	h := newHandler(store)

	// Seed three snapshots: FC 1.7 (old), 1.8 (current), 1.9 (newer).
	for _, v := range []string{"1.7.0", "1.8.0", "1.9.0"} {
		acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
		app, _ := store.CreateApp(context.Background(), state.App{
			AccountID: acct.ID, Slug: "snap-" + v, RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		})
		dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
			AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
		})
		if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
			DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
			FCVersion:  v,
			StorageKey: state.SnapMemKey(dep.ID),
		}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := h.MarkFCSnapshotsStale(context.Background(), "1.8.0")
	if err != nil {
		t.Fatal(err)
	}
	// Both 1.7 (older) AND 1.9 (newer) differ from 1.8 and must be marked
	// stale. Only the current FC version's snapshots remain live — that's
	// the whole point of the sweep (ADR-005).
	if n != 2 {
		t.Errorf("FC sweep: marked %d stale, want 2 (1.7 + 1.9)", n)
	}
}

func TestFCSweep_DetectErrorFailsOpen(t *testing.T) {
	store := state.NewMemStore()
	// Empty fc version should error (defensive guard) — and imaged's loop
	// treats this as a soft warn, never fatal.
	h := newHandler(store)
	if _, err := h.MarkFCSnapshotsStale(context.Background(), ""); err == nil {
		t.Error("expected error on empty fc version")
	}
}

func TestFCSweep_IdempotentSecondStart(t *testing.T) {
	store := state.NewMemStore()
	h := newHandler(store)

	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "idem", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
	})
	if _, err := store.CreateSnapshot(context.Background(), state.Snapshot{
		DeploymentID: dep.ID, MemBytes: 100, DiskBytes: 100,
		FCVersion:  "1.7.0",
		StorageKey: state.SnapMemKey(dep.ID),
	}); err != nil {
		t.Fatal(err)
	}

	n1, _ := h.MarkFCSnapshotsStale(context.Background(), "1.8.0")
	n2, _ := h.MarkFCSnapshotsStale(context.Background(), "1.8.0")
	if n1 != 1 || n2 != 0 {
		t.Errorf("first sweep %d, second %d; want 1 then 0 (idempotent)", n1, n2)
	}
}

// --- F3: function-runner wiring --------------------------------------------

func TestFunctionLayer_Node22_BuildsWithRunner(t *testing.T) {
	store := state.NewMemStore()
	bin := filepath.Join(t.TempDir(), "faas-runner")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bld := &fakeBuilder{}
	h := newHandlerWithBuilder(store, bld)
	h.WithFunctionRunnerNode22(bin)

	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "node", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/src.tgz",
	})
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "/tmp/oci.tar", sched.AppLayerKey(app.Slug, dep.ID), 1024); err != nil {
		t.Fatal(err)
	}
	notif := &fakeNotifier{}
	h.notif = notif

	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	}); err != nil {
		t.Fatalf("handleSnapshotBoot: %v", err)
	}
	if len(bld.calls) != 1 {
		t.Fatalf("Builder.Build calls = %d, want 1", len(bld.calls))
	}
	if bld.calls[0].FunctionRunnerPath != bin {
		t.Errorf("FunctionRunnerPath = %q, want %q", bld.calls[0].FunctionRunnerPath, bin)
	}
}

func TestFunctionLayer_EmptyRunnerPath_FailsLoud(t *testing.T) {
	store := state.NewMemStore()
	h := newHandler(store)
	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "no-runner", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/src.tgz",
	})
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "/tmp/oci.tar", sched.AppLayerKey(app.Slug, dep.ID), 1024); err != nil {
		t.Fatal(err)
	}
	err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	})
	if err == nil {
		t.Fatal("expected failure when runner path empty")
	}
	if !strings.Contains(err.Error(), "function runner binary not configured") {
		t.Errorf("err = %q, want loud 'function runner binary not configured' message", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeployFailed {
		t.Errorf("deployment status = %s, want failed", got.Status)
	}
}

func TestFunctionLayer_ImageApp_DoesNotReadRunner(t *testing.T) {
	store := state.NewMemStore()
	bld := &fakeBuilder{}
	h := newHandlerWithBuilder(store, bld)
	h.oci = fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{
		Cmd: []string{"/app/entry.sh"},
	}}
	// Wire a runner so buildFunctionLayer would use it if it ran.
	h.WithFunctionRunnerPython312("/tmp/some-runner")

	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "image-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
	})
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "/tmp/oci.tar", sched.AppLayerKey(app.Slug, dep.ID), 1024); err != nil {
		t.Fatal(err)
	}
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	}); err != nil {
		t.Fatalf("handleSnapshotBoot: %v", err)
	}
	if len(bld.calls) != 1 {
		t.Fatalf("Builder.Build calls = %d, want 1", len(bld.calls))
	}
	if bld.calls[0].FunctionRunnerPath != "" {
		t.Errorf("image-kind Build.FunctionRunnerPath = %q, want empty", bld.calls[0].FunctionRunnerPath)
	}
}

// --- F4: snapshot_boot handler ---------------------------------------------

func TestHandleSnapshotBoot_BuildsAndPrimesForTarball(t *testing.T) {
	store := state.NewMemStore()
	notif := &fakeNotifier{}
	bld := &fakeBuilder{bytesOut: 4096}
	bin := filepath.Join(t.TempDir(), "faas-runner")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHandlerWithBuilder(store, bld)
	h.notif = notif
	h.WithFunctionRunnerNode22(bin)

	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "boot-app", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
		Runtime: RuntimeNode22,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/tmp/x.tgz",
	})
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "/tmp/oci.tar", sched.AppLayerKey(app.Slug, dep.ID), 4096); err != nil {
		t.Fatal(err)
	}
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	}); err != nil {
		t.Fatalf("handleSnapshotBoot: %v", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status != state.DeploySnapshotting {
		t.Errorf("deployment status = %s, want snapshotting", got.Status)
	}
	if !strings.HasSuffix(got.RootfsPath, ".ext4") {
		t.Errorf("RootfsPath = %q, want .ext4", got.RootfsPath)
	}
	primeFound := false
	for _, c := range notif.calls {
		if c.channel == db.NotifySnapshotPrime &&
			strings.Contains(c.payload, app.ID) &&
			strings.Contains(c.payload, dep.ID) {
			primeFound = true
		}
	}
	if !primeFound {
		t.Errorf("expected NotifySnapshotPrime to schedd; got %v", notif.calls)
	}
}

func TestHandleSnapshotBoot_RedeliverySafe(t *testing.T) {
	store := state.NewMemStore()
	bld := &fakeBuilder{}
	h := newHandlerWithBuilder(store, bld)
	h.oci = fakePuller{digest: "sha256:abc", cfg: oci.ImageConfig{
		Cmd: []string{"/app/entry.sh"},
	}}

	acct, _ := store.CreateAccount(context.Background(), "u@example.com", "pro")
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID, Slug: "redel", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc",
	})
	if err := store.SetDeploymentRootfs(context.Background(), dep.ID, "/tmp/oci.tar", sched.AppLayerKey(app.Slug, dep.ID), 1024); err != nil {
		t.Fatal(err)
	}
	// First call: progresses to snapshotting.
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	}); err != nil {
		t.Fatal(err)
	}
	buildCalls := len(bld.calls)

	// Second call (redelivery): bails on the status guard.
	if err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	}); err != nil {
		t.Errorf("redelivery should be a silent no-op, got %v", err)
	}
	if len(bld.calls) != buildCalls {
		t.Errorf("redelivery caused extra Build call: %d -> %d", buildCalls, len(bld.calls))
	}
}

// TestHandleSnapshotBoot_EmptyRootfsPath_NoOp is the F-01 companion
// expectation: handleSnapshotBoot with an empty rootfs_path bails to
// log.Warn + return nil so that the deployment is NOT marked failed.
// The canonical path is builderd's later NotifySnapshotBoot emit which
// arrives AFTER the rootfs_path stamp. Prior to F-01 this exact case
// transitioned the deployment to DeployFailed with "empty rootfs_path
// (builderd didn't stamp)", blocking every tarball deploy on day one.
// (Renamed from TestHandleSnapshotBoot_MissingTarballFailsLoud.)
func TestHandleSnapshotBoot_EmptyRootfsPath_NoOp(t *testing.T) {
	store := state.NewMemStore()
	h := newHandler(store)
	app, _ := store.CreateApp(context.Background(), state.App{
		AccountID: "acct", Slug: "missing-tar", RAMMB: 256, IdleTimeoutS: 30, MaxConcurrency: 2,
	})
	dep, _, _ := store.CreateDeployment(context.Background(), state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindTarball,
		// RootfsPath intentionally empty.
	})
	err := h.handleSnapshotBoot(context.Background(), snapshotBootPayload{
		AppID: app.ID, DeploymentID: dep.ID,
	})
	if err != nil {
		t.Fatalf("F-01 reversal: empty rootfs_path must be a transient no-op, not an error (got %v)", err)
	}
	got, _ := store.DeploymentByID(context.Background(), dep.ID)
	if got.Status == state.DeployFailed {
		t.Errorf("F-01 reversal: empty rootfs_path must NOT transition to failed; status=%s", got.Status)
	}
}

// --- helpers ---------------------------------------------------------------

func newHandler(store *state.MemStore) *Handler {
	return &Handler{
		store:         store,
		oci:           fakePuller{},
		builder:       &fakeBuilder{},
		guestInitPath: "./init",
		appsRoot:      filepath.Join(os.TempDir(), "imaged-test-apps"),
		log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		notif:         &fakeNotifier{},
	}
}

func newHandlerWithBuilder(store *state.MemStore, b *fakeBuilder) *Handler {
	h := newHandler(store)
	h.builder = b
	return h
}
