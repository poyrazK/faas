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
