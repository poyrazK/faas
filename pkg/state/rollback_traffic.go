package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
)

// RollbackTrafficStore restores a retained artifact to all traffic only while
// the observed deployment still owns the scope. Readiness, traffic and the
// existing promotion side effects share the store's mutation boundary. Scopes
// with enforced binding policy use the checked historical rollback workflow.
type RollbackTrafficStore interface {
	RestoreDeploymentTrafficIfCurrent(context.Context, string, string, string) error
}

type rollbackTrafficSelection struct{ appID, currentID string }
type rollbackTrafficKey struct{}

func rollbackTrafficContext(ctx context.Context, app, current string) context.Context {
	return context.WithValue(ctx, rollbackTrafficKey{}, rollbackTrafficSelection{app, current})
}

func (m *MemStore) RestoreDeploymentTrafficIfCurrent(ctx context.Context, app, target, current string) error {
	return m.MarkDeploymentLive(rollbackTrafficContext(ctx, app, current), target)
}

func (s *PgStore) RestoreDeploymentTrafficIfCurrent(ctx context.Context, app, target, current string) error {
	return s.MarkDeploymentLive(rollbackTrafficContext(ctx, app, current), target)
}

func rollbackTrafficTargetValid(d Deployment, selection rollbackTrafficSelection) bool {
	return selection.appID != "" && selection.currentID != "" && d.AppID == selection.appID &&
		d.ID != selection.currentID && d.DeletedAt == nil && !d.EnvironmentWorkloadHeld() &&
		(d.Status == DeploySuperseded || d.Status == DeployLive && d.TrafficPercent == 0)
}

// Only the local candidate is reset. A failed promotion leaves the retained
// deployment and its traffic unchanged; no prepared marker escapes a failure.
func resetRollbackTrafficTarget(d Deployment) Deployment {
	d.Status, d.Error, d.ErrorCode = DeploySnapshotting, "", ""
	d.TrafficPercent, d.TrafficPercentExplicit = 0, false
	d.CanaryPreset, d.CanaryStep, d.CanaryTotalSteps, d.CanaryStages = "none", 0, 0, nil
	d.RolloutState, d.RolloutStartedAt, d.RolloutCompletedAt = "pending", nil, nil
	d.RolloutAbortedAt, d.RolloutAbortedReason = nil, ""
	return d
}

func (m *MemStore) rollbackTrafficTargetLocked(ctx context.Context, d Deployment) (Deployment, error) {
	selection, ok := ctx.Value(rollbackTrafficKey{}).(rollbackTrafficSelection)
	if !ok {
		return d, nil
	}
	if !rollbackTrafficTargetValid(d, selection) {
		return d, ErrCheckedRollbackChanged
	}
	if _, active := m.checkedRollbackForTargetLocked(d.ID); active {
		return d, ErrCheckedRollbackRequired
	}
	if err := m.rejectUncheckedBindingReleaseLocked(d.AppID, d.Scope); err != nil {
		return d, err
	}
	r := api.RollbackOperation{AppID: d.AppID, Scope: normalizedDeploymentScope(d.Scope), TargetDeploymentID: d.ID, CurrentDeploymentID: selection.currentID}
	if !rollbackPairValid(r, d, m.deployments[selection.currentID], m.rollbackRowsLocked(r)) {
		return d, ErrCheckedRollbackChanged
	}
	return resetRollbackTrafficTarget(d), nil
}
