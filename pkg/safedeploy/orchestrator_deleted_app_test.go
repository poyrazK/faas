package safedeploy

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 14 H14-84: h11-py was deleted four minutes into its canary and its
// deployment stayed rolling_out for six days; every tick asked APID to abort
// it, and APID refused to load a deleted app's rollout. A stuck rollout of a
// deleted app is now closed as aborted without a recovery call.
func TestOrchestrator_StuckRolloutOfDeletedAppIsClosed(t *testing.T) {
	store := newStubStore()
	stuckAt := time.Now().Add(-2 * StuckAfterDuration)
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 1
		d.CanaryStepStartedAt = &stuckAt
		d.RolloutStartedAt = &stuckAt
	})
	recovery := &rolloutRecoveryStub{}
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")
	o.Targets = rolloutTargetStub{app: state.App{ID: dep.AppID, Slug: "h11-py", Status: state.AppDeleted}}
	o.Recovery = recovery

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if recovery.calls != 0 {
		t.Fatalf("recovery called %d times for a deleted app; want 0", recovery.calls)
	}
	if stats.AutoAborted != 1 || stats.AutoAbortFailed != 0 {
		t.Fatalf("stats = %+v; want one auto-aborted, none failed", stats)
	}
	got := store.rollouts[dep.ID]
	if got.RolloutState != "aborted" || got.RolloutAbortedAt == nil || got.RolloutAbortedReason != "app deleted during rollout" {
		t.Fatalf("rollout = state %q aborted_at %v reason %q; want aborted for the deleted app", got.RolloutState, got.RolloutAbortedAt, got.RolloutAbortedReason)
	}
	if got.RolloutStartedAt == nil || !got.RolloutStartedAt.Equal(stuckAt) {
		t.Fatalf("rollout_started_at = %v; want the original start kept", got.RolloutStartedAt)
	}
	if n := len(store.auditRows); n != 1 || store.auditRows[0].Kind != state.DeploymentAuditKind(AuditKindRolloutAborted) {
		t.Fatalf("audit rows = %+v; want one %s", store.auditRows, AuditKindRolloutAborted)
	}

	// The next tick no longer picks the row.
	stats, _, err = o.Once(context.Background())
	if err != nil {
		t.Fatalf("second Once: %v", err)
	}
	if stats.StuckDetected != 0 || stats.AutoAborted != 0 || recovery.calls != 0 {
		t.Fatalf("second tick stats = %+v, recovery calls %d; want the closed row left alone", stats, recovery.calls)
	}
}

// ADR-20261028110044 amendment 8: dapi-t1109-probe was deleted a minute into
// its canary and meterd evaluated it against the missing app for 30 minutes
// until the stuck threshold. A deleted app's rollout closes on the next tick.
func TestOrchestrator_DeletedAppRolloutClosesBeforeStuckThreshold(t *testing.T) {
	store := newStubStore()
	started := time.Now().Add(-time.Minute)
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 1
		d.CanaryStepStartedAt = &started
		d.RolloutStartedAt = &started
	})
	recovery := &rolloutRecoveryStub{}
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")
	o.Targets = rolloutTargetStub{app: state.App{ID: dep.AppID, Slug: "dapi-t1109-probe", Status: state.AppDeleted}}
	o.Recovery = recovery

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.StuckDetected != 0 || stats.AutoAborted != 1 || recovery.calls != 0 {
		t.Fatalf("stats = %+v, recovery calls %d; want the rollout closed without a stuck detection", stats, recovery.calls)
	}
	if got := store.rollouts[dep.ID]; got.RolloutState != "aborted" || got.RolloutAbortedReason != "app deleted during rollout" {
		t.Fatalf("rollout = state %q reason %q; want aborted for the deleted app", got.RolloutState, got.RolloutAbortedReason)
	}
}

// A live app's young rollout is left to the progression worker.
func TestOrchestrator_LiveAppRolloutIsNotClosedEarly(t *testing.T) {
	store := newStubStore()
	started := time.Now().Add(-time.Minute)
	dep := seedDeployment(store, t, func(d *state.Deployment) {
		d.RolloutState = "rolling_out"
		d.CanaryStep = 1
		d.CanaryStepStartedAt = &started
		d.RolloutStartedAt = &started
	})
	o := NewOrchestrator(store, discardLog(), "meterd:safedeploy", "")
	o.Targets = rolloutTargetStub{app: state.App{ID: dep.AppID, Slug: "api", Status: state.AppActive}}
	o.Recovery = &rolloutRecoveryStub{}

	stats, _, err := o.Once(context.Background())
	if err != nil {
		t.Fatalf("Once: %v", err)
	}
	if stats.AutoAborted != 0 {
		t.Fatalf("stats = %+v; want the live app's rollout left running", stats)
	}
}
