package sched_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedReadyParentCapture seeds an opted-in branch whose lineage parent has a
// ready credential-free capture, and returns the capture.
func seedReadyParentCapture(t *testing.T, store *state.MemStore, optIn bool) (state.App, state.Deployment, state.EnvironmentBranchCapture) {
	t.Helper()
	ctx := context.Background()
	app, dep := seedEnvironmentBranchWith(t, store, api.ExecutionModeRequest, false, func(manifest *state.AppManifest) {
		if optIn {
			manifest.AfterRestore = &api.AfterRestoreHook{Path: "/internal/after-restore", Identity: api.AfterRestoreIdentityReload}
		}
	})
	parent, err := store.EnvironmentBranchLineageParent(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestEnvironmentBranchCapture(ctx, parent); err != nil {
		t.Fatal(err)
	}
	lease := uuid.NewString()
	claimed, err := store.ClaimEnvironmentBranchCapture(ctx, lease, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	artifact := state.EnvironmentBranchCaptureArtifact{
		FCVersion: "1.10.0", StorageKey: state.SnapshotCaptureMemKey(parent, state.SnapshotTierWarm, uuid.NewString()),
		BaseImageVersion: fcvm.FAAS_BASE_IMAGE_VERSION, MemBytes: int64(app.RAMMB) << 20, DiskBytes: 1 << 20,
	}
	if err := store.PublishEnvironmentBranchCapture(ctx, claimed.ID, lease, artifact); err != nil {
		t.Fatal(err)
	}
	capture, err := store.EnvironmentBranchCapture(ctx, parent)
	if err != nil {
		t.Fatal(err)
	}
	return app, dep, capture
}

func wakeBranch(t *testing.T, store *state.MemStore, vmm *sched.FakeVMM, app state.App, dep state.Deployment) {
	t.Helper()
	ctx := context.Background()
	engine := sched.XNewEngine(t, store, vmm, &sched.FakeNotifier{}, "1.10.0")
	// imaged marks the deployment live on deployment_ready.
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Wake(ctx, app.ID, dep.ID, dep.Scope, ""); err != nil {
		t.Fatalf("Wake(branch): %v", err)
	}
}

// ADR-20261031193635 slice 5b: an opted-in branch with no snapshot of its
// own restores from its lineage parent's capture, with an identity reload,
// under its own deployment.
func TestBranchWakeRestoresTheParentsCapture(t *testing.T) {
	store := state.NewMemStore()
	app, dep, capture := seedReadyParentCapture(t, store, true)
	vmm := &sched.FakeVMM{}
	wakeBranch(t, store, vmm, app, dep)

	if *vmm.Restores() != 1 || *vmm.ColdBoots() != 0 {
		t.Fatalf("restores=%d cold=%d; want one restore", *vmm.Restores(), *vmm.ColdBoots())
	}
	ref := *vmm.LastSnapRef()
	if ref.StorageKey != capture.StorageKey || ref.DeploymentID != dep.ID {
		t.Fatalf("restore ref = %+v; want the capture key under the branch deployment", ref)
	}
	if !vmm.LastRestoreSpec().IdentityReload {
		t.Fatal("the capture restore did not ask for an identity reload")
	}
	if got, err := store.EnvironmentBranchCapture(context.Background(), capture.DeploymentID); err != nil || got.State != state.EnvironmentBranchCaptureReady {
		t.Fatalf("capture after a good restore = %+v, %v", got, err)
	}
}

// Without the opt-in a branch never restores its parent's memory.
func TestBranchWakeWithoutOptInColdBoots(t *testing.T) {
	store := state.NewMemStore()
	app, dep, _ := seedReadyParentCapture(t, store, false)
	vmm := &sched.FakeVMM{}
	wakeBranch(t, store, vmm, app, dep)
	if *vmm.Restores() != 0 || *vmm.ColdBoots() != 1 {
		t.Fatalf("restores=%d cold=%d; want a cold boot", *vmm.Restores(), *vmm.ColdBoots())
	}
}

// A restore the app did not acknowledge cold-boots and retires the capture,
// so later wakes do not pay for two boots.
func TestBranchWakeRetiresAnUnacknowledgedCapture(t *testing.T) {
	store := state.NewMemStore()
	app, dep, capture := seedReadyParentCapture(t, store, true)
	vmm := &sched.FakeVMM{}
	vmm.ForceColdFallback(fcvm.WakeReasonAfterRestoreFailed)
	wakeBranch(t, store, vmm, app, dep)

	got, err := store.EnvironmentBranchCapture(context.Background(), capture.DeploymentID)
	if err != nil || got.State != state.EnvironmentBranchCaptureFailed {
		t.Fatalf("capture after an unacknowledged restore = %+v, %v; want failed", got, err)
	}
}
