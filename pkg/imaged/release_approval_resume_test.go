package imaged

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261020085706 — amendment 13: what an approved release rechecks
// before its release command runs.

func (fx releaseGateFixture) approved(t *testing.T) {
	t.Helper()
	fx.requireApprovals(t)
	if err := fx.handler.handoffSnapshotPrime(context.Background(), fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	fx.requireNoReleaseTask(t)
	// No recorded deployer: two approvals (amendment 3).
	fx.decide(t, state.ReleaseApprovalApprove)
	fx.decide(t, state.ReleaseApprovalApprove)
}

func (fx releaseGateFixture) freezeSince(t *testing.T, created time.Time) {
	t.Helper()
	starts := created.Add(-time.Minute)
	if _, err := fx.store.CreateReleaseFreeze(context.Background(), state.ReleaseFreeze{
		AccountID: fx.account.ID, Kind: state.ReleaseFreezeWindow, StartsAt: &starts,
		Reason: "launch week", CreatedBy: "ops@example.com",
	}, 10, created); err != nil {
		t.Fatalf("CreateReleaseFreeze: %v", err)
	}
}

func (fx releaseGateFixture) status(t *testing.T) state.Deployment {
	t.Helper()
	dep, err := fx.store.DeploymentByID(context.Background(), fx.candidate.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	return dep
}

// A newer revision shipped while the release waited: approving it must not
// run its older migration against the newer schema.
func TestOutpacedReleaseNeverRunsItsReleaseCommand(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.approved(t)
	ctx := context.Background()
	if _, err := fx.store.CreateDeployment(ctx, state.Deployment{
		AppID: fx.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:newer", Status: state.DeployPending,
	}); err != nil {
		t.Fatalf("CreateDeployment(newer): %v", err)
	}
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	fx.requireNoReleaseTask(t)
	if dep := fx.status(t); dep.Status != state.DeploySuperseded {
		t.Fatalf("outpaced release = %s, want superseded", dep.Status)
	}
}

func TestApprovedReleaseWaitsOutTheReleasePause(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.approved(t)
	ctx := context.Background()
	paused := true
	fx.handler.WithReleasePause(func() bool { return paused })
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold while paused: %v", err)
	}
	fx.requireNoReleaseTask(t)
	if dep := fx.status(t); dep.Status != state.DeploySnapshotting {
		t.Fatalf("paused release = %s, want snapshotting", dep.Status)
	}
	paused = false
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold after the pause: %v", err)
	}
	if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err != nil {
		t.Fatalf("release task after the pause: %v", err)
	}
}

func TestApprovedReleaseStopsAtAFreezeStartedSinceItWasDeployed(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.approved(t)
	fx.freezeSince(t, time.Now().UTC())
	if err := fx.handler.resumeReleaseApprovalHold(context.Background(), fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	fx.requireNoReleaseTask(t)
	if dep := fx.status(t); dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeReleaseFrozen {
		t.Fatalf("frozen release = %s/%q, want failed/%s", dep.Status, dep.ErrorCode, api.CodeReleaseFrozen)
	}
}

// amendment 14: a scheduled release whose schedule ended while it waited for
// approval does not run its release command at some later, unplanned time.
func TestHeldReleaseClosesWhenItsScheduleEnded(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.requireApprovals(t)
	ctx := context.Background()
	now := time.Now().UTC()
	sr, _, err := fx.store.ScheduleRelease(ctx, state.ScheduledRelease{
		AccountID: fx.account.ID, AppID: fx.app.ID, DeploymentID: fx.candidate.ID,
		ReleaseAt: now.Add(time.Hour), ScheduledBy: "release@example.com",
	}, "", "", now)
	if err != nil {
		t.Fatalf("ScheduleRelease: %v", err)
	}
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	fx.requireNoReleaseTask(t)
	if dep := fx.status(t); dep.Status != state.DeploySnapshotting {
		t.Fatalf("release with a pending schedule = %s, want waiting", dep.Status)
	}
	if _, err := fx.store.FinishScheduledRelease(ctx, sr.ID, state.ScheduledReleaseExpired, "not_ready", "expired", now); err != nil {
		t.Fatalf("FinishScheduledRelease: %v", err)
	}
	fx.decide(t, state.ReleaseApprovalApprove)
	fx.decide(t, state.ReleaseApprovalApprove)
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	fx.requireNoReleaseTask(t)
	if dep := fx.status(t); dep.Status != state.DeployFailed {
		t.Fatalf("release whose schedule ended = %s, want failed", dep.Status)
	}
}

// amendment 14: a newer release that failed ships nothing, so it does not
// outpace one that waited for approval.
func TestFailedNewerReleaseDoesNotOutpaceAWaitingOne(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.approved(t)
	ctx := context.Background()
	newer, err := fx.store.CreateDeployment(ctx, state.Deployment{
		AppID: fx.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:broken", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(newer): %v", err)
	}
	if _, err := fx.store.SetDeploymentFailed(ctx, newer.ID, "build_failed", "the build failed"); err != nil {
		t.Fatalf("SetDeploymentFailed(newer): %v", err)
	}
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err != nil {
		t.Fatalf("release task: %v; a failed newer release must not supersede it", err)
	}
}

// A freeze in force when the release was deployed was overridden then; it
// does not stop the release now.
func TestApprovedReleaseKeepsTheFreezeItOverrode(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.freezeSince(t, fx.candidate.CreatedAt.Add(-time.Minute))
	fx.approved(t)
	ctx := context.Background()
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err != nil {
		t.Fatalf("release task: %v", err)
	}
}
