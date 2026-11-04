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
