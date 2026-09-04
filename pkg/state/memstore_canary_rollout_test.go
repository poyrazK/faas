package state

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func seedCanaryDeployment(m *MemStore, d Deployment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deployments[d.ID] = d
}

func TestMemStore_CanaryRolloutListingsAndStamp(t *testing.T) {
	m, ctx, _, _, dep := memDeploymentFixture(t)
	base := time.Now().Add(-2 * time.Hour)
	started := base.Add(time.Minute)

	dep.Status = DeployLive
	dep.CanaryTotalSteps = 4
	dep.CanaryStep = 1
	dep.RolloutState = "pending"
	dep.CreatedAt = base
	seedCanaryDeployment(m, dep)

	rolling := dep
	rolling.ID = uuid.NewString()
	rolling.CanaryStep = 2
	rolling.RolloutState = "rolling_out"
	rolling.RolloutStartedAt = &started
	rolling.CreatedAt = base.Add(time.Minute)
	seedCanaryDeployment(m, rolling)

	complete := dep
	complete.ID = uuid.NewString()
	complete.CanaryStep = complete.CanaryTotalSteps
	complete.RolloutState = "complete"
	complete.CreatedAt = base.Add(2 * time.Minute)
	seedCanaryDeployment(m, complete)

	zeroStep := dep
	zeroStep.ID = uuid.NewString()
	zeroStep.CanaryTotalSteps = 0
	zeroStep.CreatedAt = base.Add(3 * time.Minute)
	seedCanaryDeployment(m, zeroStep)

	nonLive := dep
	nonLive.ID = uuid.NewString()
	nonLive.Status = DeployBuilding
	nonLive.CreatedAt = base.Add(4 * time.Minute)
	seedCanaryDeployment(m, nonLive)

	inFlight, err := m.ListCanaryInFlight(ctx)
	if err != nil {
		t.Fatalf("ListCanaryInFlight: %v", err)
	}
	if len(inFlight) != 2 || inFlight[0].ID != dep.ID || inFlight[1].ID != rolling.ID {
		t.Fatalf("ListCanaryInFlight = %#v, want pending and rolling rows in creation order", inFlight)
	}

	pending, err := m.SafedeployListPendingRollouts(ctx)
	if err != nil {
		t.Fatalf("SafedeployListPendingRollouts: %v", err)
	}
	if len(pending) != 3 || pending[0].ID != dep.ID || pending[1].ID != zeroStep.ID || pending[2].ID != rolling.ID {
		t.Fatalf("SafedeployListPendingRollouts = %#v, want NULL-start rows first, then rolling row", pending)
	}

	completedAt := base.Add(5 * time.Minute)
	abortedAt := base.Add(6 * time.Minute)
	stamped, err := m.SafedeployStampRollout(ctx, dep.ID, "aborted", &started, &completedAt, &abortedAt, "operator test")
	if err != nil {
		t.Fatalf("SafedeployStampRollout: %v", err)
	}
	if stamped.RolloutState != "aborted" || stamped.RolloutStartedAt == nil || stamped.RolloutCompletedAt == nil || stamped.RolloutAbortedAt == nil || stamped.RolloutAbortedReason != "operator test" {
		t.Fatalf("stamped deployment = %#v, want all rollout fields updated", stamped)
	}
	if _, err := m.SafedeployStampRollout(ctx, "missing", "aborted", nil, nil, nil, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing SafedeployStampRollout error = %v, want ErrNotFound", err)
	}
}

func TestMemStore_RecoverRolloutActionsAndGuards(t *testing.T) {
	m, ctx, _, app, dep := memDeploymentFixture(t)
	old := time.Now().Add(-RecoverRolloutStuckAfter - time.Minute)
	dep.Status = DeployLive
	dep.CanaryPreset = "balanced"
	dep.CanaryStep = 3
	dep.CanaryTotalSteps = 4
	dep.CanaryStepStartedAt = &old
	dep.RolloutState = "rolling_out"
	dep.TrafficPercent = 50
	seedCanaryDeployment(m, dep)

	sibling := dep
	sibling.ID = uuid.NewString()
	sibling.CanaryStep = 0
	sibling.CanaryTotalSteps = 0
	sibling.CanaryStepStartedAt = nil
	sibling.RolloutState = "complete"
	sibling.TrafficPercent = 50
	seedCanaryDeployment(m, sibling)

	if _, _, err := m.RecoverRollout(ctx, app.ID, "bad", ""); !errors.Is(err, ErrInvalidRecoverAction) {
		t.Fatalf("invalid action error = %v, want ErrInvalidRecoverAction", err)
	}

	advanced, auditID, err := m.RecoverRollout(ctx, app.ID, "advance", "stuck test")
	if err != nil {
		t.Fatalf("advance: %v", err)
	}
	if advanced.CanaryStep != 4 || advanced.TrafficPercent != 100 || advanced.RolloutState != "complete" || advanced.RolloutCompletedAt == nil || auditID != 1 {
		t.Fatalf("advanced deployment = %#v, auditID=%d; want final step, complete, and audit 1", advanced, auditID)
	}

	// Re-open the same row to exercise promote independently of the
	// stuck-duration guard; promote is the explicit force-complete path.
	advanced.RolloutState = "rolling_out"
	advanced.CanaryStep = 1
	advanced.CanaryTotalSteps = 4
	advanced.TrafficPercent = 1
	advanced.RolloutCompletedAt = nil
	seedCanaryDeployment(m, advanced)

	promoted, auditID, err := m.RecoverRollout(ctx, app.ID, "promote", "operator test")
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if promoted.CanaryStep != promoted.CanaryTotalSteps || promoted.RolloutState != "complete" || promoted.TrafficPercent != 100 || promoted.RolloutCompletedAt == nil || auditID != 2 {
		t.Fatalf("promoted deployment = %#v, auditID=%d; want complete at 100%% and audit 2", promoted, auditID)
	}
	if got, err := m.DeploymentByID(ctx, sibling.ID); err != nil || got.TrafficPercent != 0 {
		t.Fatalf("sibling after promote = %#v, %v; want traffic 0", got, err)
	}

	// Abort is valid from either active rollout state and records a
	// separate rollback audit.
	promoted.RolloutState = "pending"
	promoted.CanaryStep = 1
	promoted.CanaryTotalSteps = 4
	seedCanaryDeployment(m, promoted)
	aborted, auditID, err := m.RecoverRollout(ctx, app.ID, "abort", "manual stop")
	if err != nil {
		t.Fatalf("abort: %v", err)
	}
	if aborted.RolloutState != "aborted" || aborted.RolloutAbortedAt == nil || aborted.RolloutAbortedReason != "manual stop" || auditID != 3 {
		t.Fatalf("aborted deployment = %#v, auditID=%d; want aborted with reason and audit 3", aborted, auditID)
	}
	if aborted.TrafficPercent != 0 {
		t.Errorf("aborted traffic = %d, want 0", aborted.TrafficPercent)
	}
	if got, err := m.DeploymentByID(ctx, sibling.ID); err != nil || got.TrafficPercent != 100 {
		t.Fatalf("sibling after abort = %#v, %v; want traffic 100", got, err)
	}

	audits, err := m.ListDeploymentAudit(ctx, uuid.MustParse(dep.ID).String(), 0)
	if err != nil {
		t.Fatalf("ListDeploymentAudit: %v", err)
	}
	if len(audits) != 3 || audits[0].Kind != DeployRolledBack || audits[1].Kind != DeployTrafficChanged {
		t.Fatalf("recovery audits = %#v, want newest rollback followed by traffic changes", audits)
	}
}

func TestMemStore_RecoverRolloutErrorGuards(t *testing.T) {
	noRollout, ctx, _, app, _ := memDeploymentFixture(t)
	if _, _, err := noRollout.RecoverRollout(ctx, app.ID, "abort", "none"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("no active rollout error = %v, want ErrNotFound", err)
	}

	notStuck, ctx, _, app, dep := memDeploymentFixture(t)
	recent := time.Now()
	dep.Status = DeployLive
	dep.RolloutState = "rolling_out"
	dep.CanaryStep = 1
	dep.CanaryTotalSteps = 4
	dep.CanaryStepStartedAt = &recent
	seedCanaryDeployment(notStuck, dep)
	if _, _, err := notStuck.RecoverRollout(ctx, app.ID, "advance", "too soon"); !errors.Is(err, ErrRolloutNotStuck) {
		t.Fatalf("not-stuck error = %v, want ErrRolloutNotStuck", err)
	}

	invalidState, ctx, _, app, dep := memDeploymentFixture(t)
	old := time.Now().Add(-RecoverRolloutStuckAfter - time.Minute)
	dep.Status = DeployLive
	dep.RolloutState = "rolling_out"
	dep.CanaryStepStartedAt = &old
	dep.CanaryTotalSteps = 0
	seedCanaryDeployment(invalidState, dep)
	if _, _, err := invalidState.RecoverRollout(ctx, app.ID, "advance", "bad state"); !errors.Is(err, ErrRolloutStateInvalid) {
		t.Fatalf("invalid advance state error = %v, want ErrRolloutStateInvalid", err)
	}
	if _, _, err := invalidState.RecoverRollout(ctx, app.ID, "promote", "bad state"); !errors.Is(err, ErrRolloutStateInvalid) {
		t.Fatalf("invalid promote state error = %v, want ErrRolloutStateInvalid", err)
	}
}

func TestStepToPercentAndSiblingAdapter(t *testing.T) {
	tests := []struct {
		step, total, want int
	}{
		{0, 4, 0},
		{1, 2, 1},
		{2, 2, 100},
		{1, 3, 5},
		{2, 3, 50},
		{3, 3, 100},
		{1, 4, 1},
		{2, 4, 10},
		{3, 4, 50},
		{4, 4, 100},
		{5, 4, 0},
		{1, 9, 0},
		{1, 0, 0},
	}
	for _, tc := range tests {
		if got := stepToPercent(tc.step, tc.total); got != tc.want {
			t.Errorf("stepToPercent(%d, %d) = %d, want %d", tc.step, tc.total, got, tc.want)
		}
	}

	got := toHelperSiblings([]siblingRow{{ID: "b", Prior: 20}, {ID: "a", Prior: 80}})
	if len(got) != 2 || got[0].ID != "b" || got[0].Prior != 20 || got[1].ID != "a" || got[1].Prior != 80 {
		t.Fatalf("toHelperSiblings = %#v, want copied IDs and weights", got)
	}
}
