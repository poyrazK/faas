package imaged

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1448 — amendment 1; 20261022140000 — amendment 2.

func (fx releaseGateFixture) newerRelease(t *testing.T, digest string) state.Deployment {
	t.Helper()
	ctx := context.Background()
	d, err := fx.store.CreateDeployment(ctx, state.Deployment{
		AppID: fx.app.ID, Kind: state.DeploymentKindImage, ImageDigest: digest,
		Status: state.DeploySnapshotting, ReleaseCommand: []string{"bin/migrate"},
	})
	if err != nil {
		t.Fatalf("CreateDeployment(%s): %v", digest, err)
	}
	if err := fx.store.SetDeploymentRootfs(ctx, d.ID, "/tmp/"+d.ID+".ext4", "apps/release-gate/"+d.ID+".ext4", 4096); err != nil {
		t.Fatalf("SetDeploymentRootfs(%s): %v", digest, err)
	}
	if d, err = fx.store.DeploymentByID(ctx, d.ID); err != nil {
		t.Fatalf("DeploymentByID(%s): %v", digest, err)
	}
	return d
}

func (fx releaseGateFixture) releaseTask(t *testing.T, deploymentID string) state.AppTask {
	t.Helper()
	task, err := fx.store.ReleaseAppTaskByDeployment(context.Background(), deploymentID)
	if err != nil {
		t.Fatalf("ReleaseAppTaskByDeployment(%s): %v", deploymentID, err)
	}
	return task
}

func (fx releaseGateFixture) deployment(t *testing.T, id string) state.Deployment {
	t.Helper()
	d, err := fx.store.DeploymentByID(context.Background(), id)
	if err != nil {
		t.Fatalf("DeploymentByID(%s): %v", id, err)
	}
	return d
}

// A release created before the break-glass pause still builds, but its
// release command (a migration) waits for the pause to be lifted, and the
// stale sweep leaves it alone meanwhile.
func TestReleaseCommandWaitsOutTheReleasePause(t *testing.T) {
	fx := seedReleaseGate(t, true)
	ctx := context.Background()
	paused := true
	fx.handler.WithReleasePause(func() bool { return paused })
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime while paused: %v", err)
	}
	fx.requireNoReleaseTask(t)
	if dep := fx.status(t); dep.Status != state.DeploySnapshotting {
		t.Fatalf("paused release = %s, want snapshotting", dep.Status)
	}
	loop := &Loop{store: fx.store, handler: fx.handler, log: silentLogger()}
	if !loop.releaseApprovalPending(ctx, fx.status(t)) {
		t.Fatal("the stale sweep would cancel a release the pause holds")
	}
	paused = false
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold after the pause: %v", err)
	}
	fx.releaseTask(t, fx.candidate.ID)
}

// A newer release whose command is admitted retires an older one whose
// command is still queued: the older migration never runs.
func TestNewerReleaseCommandRetiresAQueuedOlderOne(t *testing.T) {
	fx := seedReleaseGate(t, true)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime(older): %v", err)
	}
	older := fx.releaseTask(t, fx.candidate.ID)
	newer := fx.newerRelease(t, "sha256:newer")
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, newer); err != nil {
		t.Fatalf("handoffSnapshotPrime(newer): %v", err)
	}
	fx.releaseTask(t, newer.ID)
	if dep := fx.status(t); dep.Status != state.DeploySuperseded {
		t.Fatalf("older release = %s, want superseded", dep.Status)
	}
	if task := fx.releaseTask(t, older.DeploymentID); task.Status != state.AppTaskCancelled {
		t.Fatalf("older release command = %s, want cancelled", task.Status)
	}
}

// A release command whose worker was lost may have changed the database
// partly: the next release fails before its own command runs, until a person
// records a check.
func TestReleaseCommandRefusedAfterAnUncertainOne(t *testing.T) {
	fx := seedReleaseGate(t, true)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime(lost): %v", err)
	}
	now := time.Now().UTC()
	claimed, err := fx.store.ClaimNextAppTask(ctx, "schedd-a", now, time.Second)
	if err != nil {
		t.Fatalf("ClaimNextAppTask: %v", err)
	}
	if _, err := fx.store.MarkAppTaskRunning(ctx, claimed.ID, *claimed.LeaseToken, now.Add(100*time.Millisecond)); err != nil {
		t.Fatalf("MarkAppTaskRunning: %v", err)
	}
	if _, err := fx.store.SweepExpiredAppTasks(ctx, now.Add(5*time.Second)); err != nil {
		t.Fatalf("SweepExpiredAppTasks: %v", err)
	}
	next := fx.newerRelease(t, "sha256:next")
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, next); err != nil {
		t.Fatalf("handoffSnapshotPrime(next): %v", err)
	}
	if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, next.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("release task after an uncertain one = %v, want none", err)
	}
	if dep := fx.deployment(t, next.ID); dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeReleaseOutcomeUncertain {
		t.Fatalf("next release = %s/%q, want failed/%s", dep.Status, dep.ErrorCode, api.CodeReleaseOutcomeUncertain)
	}
	if _, err := fx.store.RecordReleaseOutcomeCheck(ctx, state.ReleaseOutcomeCheck{
		TaskID: claimed.ID, AccountID: fx.account.ID, CheckedBy: "ops@example.com", Note: "migration 42 applied",
	}); err != nil {
		t.Fatalf("RecordReleaseOutcomeCheck: %v", err)
	}
	again := fx.newerRelease(t, "sha256:again")
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, again); err != nil {
		t.Fatalf("handoffSnapshotPrime(again): %v", err)
	}
	fx.releaseTask(t, again.ID)
}
