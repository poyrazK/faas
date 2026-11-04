package imaged

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// requireApprovals gives the fixture's app a policy older than its candidate,
// so the candidate's production release is gated (ADR-20261020085706).
func (fx releaseGateFixture) requireApprovals(t *testing.T) {
	t.Helper()
	if _, err := fx.store.SetReleaseApprovalPolicy(context.Background(), state.ReleaseApprovalPolicy{
		AppID: fx.app.ID, AccountID: fx.account.ID, RequiredApprovals: 1, AfterStage: 1,
		TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin,
	}, fx.candidate.CreatedAt.Add(-time.Hour)); err != nil {
		t.Fatalf("SetReleaseApprovalPolicy: %v", err)
	}
}

func (fx releaseGateFixture) decide(t *testing.T, decision string) {
	t.Helper()
	if _, err := fx.store.RecordReleaseApproval(context.Background(), state.ReleaseApproval{
		AccountID: fx.account.ID, AppID: fx.app.ID, DeploymentID: fx.candidate.ID,
		ApproverAccountID: uuid.NewString(), Approver: "reviewer@example.com",
		ApproverRole: string(state.OrgRoleAdmin), Decision: decision,
	}, time.Now()); err != nil {
		t.Fatalf("RecordReleaseApproval: %v", err)
	}
}

func (fx releaseGateFixture) requireNoReleaseTask(t *testing.T) {
	t.Helper()
	if task, err := fx.store.ReleaseAppTaskByDeployment(context.Background(), fx.candidate.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("release task = %+v, err = %v; want none before approval", task, err)
	}
	if findNotify(fx.notifier, db.NotifySnapshotPrime) != nil {
		t.Fatal("snapshot_prime emitted before approval")
	}
}

// ADR-20261020085706 amendment 12: a gated release's release command (a
// migration) waits for approval instead of running before its canary waits.
func TestReleaseCommandWaitsForApproval(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.requireApprovals(t)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	fx.requireNoReleaseTask(t)
	// A replay or sweep before any decision holds it again.
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	fx.requireNoReleaseTask(t)
	dep, err := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if err != nil || dep.Status != state.DeploySnapshotting {
		t.Fatalf("held deployment = %s, err = %v; want snapshotting", dep.Status, err)
	}

	// No recorded deployer: the release needs one more approval than the
	// policy's (amendment 3), so one approval still holds it.
	fx.decide(t, state.ReleaseApprovalApprove)
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold after one approval: %v", err)
	}
	fx.requireNoReleaseTask(t)
	fx.decide(t, state.ReleaseApprovalApprove)
	// The sweep resumes it the way a replayed handoff would.
	if err := fx.handler.handleSnapshotBoot(ctx, snapshotBootPayload{AppID: fx.app.ID, DeploymentID: fx.candidate.ID}); err != nil {
		t.Fatalf("handleSnapshotBoot after approval: %v", err)
	}
	task, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID)
	if err != nil || task.Status != state.AppTaskQueued {
		t.Fatalf("release task after approval = %+v, err = %v; want queued", task, err)
	}
}

func TestReleaseCommandRejectedFailsWithoutRunning(t *testing.T) {
	fx := seedReleaseGate(t, true)
	fx.requireApprovals(t)
	ctx := context.Background()
	if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
		t.Fatalf("handoffSnapshotPrime: %v", err)
	}
	fx.decide(t, state.ReleaseApprovalReject)
	if err := fx.handler.resumeReleaseApprovalHold(ctx, fx.candidate); err != nil {
		t.Fatalf("resumeReleaseApprovalHold: %v", err)
	}
	fx.requireNoReleaseTask(t)
	dep, err := fx.store.DeploymentByID(ctx, fx.candidate.ID)
	if err != nil || dep.Status != state.DeployFailed || dep.ErrorCode != api.CodeReleaseApprovalRejected {
		t.Fatalf("rejected deployment = %s/%q, err = %v; want failed/%s", dep.Status, dep.ErrorCode, err, api.CodeReleaseApprovalRejected)
	}
	previous, err := fx.store.DeploymentByID(ctx, fx.previous.ID)
	if err != nil || previous.Status != state.DeployLive {
		t.Fatalf("serving release = %s, err = %v; want it still live", previous.Status, err)
	}
}

// The app's first production release has no canary to wait in, and releases
// the policy does not gate run their command at once, as before.
func TestReleaseCommandRunsWhenNotGated(t *testing.T) {
	t.Run("no policy", func(t *testing.T) {
		fx := seedReleaseGate(t, true)
		ctx := context.Background()
		if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
			t.Fatalf("handoffSnapshotPrime: %v", err)
		}
		if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err != nil {
			t.Fatalf("release task: %v", err)
		}
	})
	t.Run("staging", func(t *testing.T) {
		fx := seedReleaseGate(t, true)
		fx.requireApprovals(t)
		ctx := context.Background()
		staged := fx.candidate
		staged.Scope = "staging"
		if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, staged); err != nil {
			t.Fatalf("handoffSnapshotPrime: %v", err)
		}
		if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err != nil {
			t.Fatalf("release task: %v", err)
		}
	})
	t.Run("policy set after the release started", func(t *testing.T) {
		fx := seedReleaseGate(t, true)
		ctx := context.Background()
		if _, err := fx.store.SetReleaseApprovalPolicy(ctx, state.ReleaseApprovalPolicy{
			AppID: fx.app.ID, AccountID: fx.account.ID, RequiredApprovals: 1, AfterStage: 1,
			TimeoutMinutes: 60, Approvers: state.ReleaseApproversAdmin,
		}, fx.candidate.CreatedAt.Add(time.Minute)); err != nil {
			t.Fatalf("SetReleaseApprovalPolicy: %v", err)
		}
		if err := fx.handler.handoffSnapshotPrime(ctx, fx.app, fx.candidate); err != nil {
			t.Fatalf("handoffSnapshotPrime: %v", err)
		}
		if _, err := fx.store.ReleaseAppTaskByDeployment(ctx, fx.candidate.ID); err != nil {
			t.Fatalf("release task: %v", err)
		}
	})
}
