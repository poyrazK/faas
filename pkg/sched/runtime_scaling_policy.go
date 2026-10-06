package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) runtimeScalingStateForDeployment(ctx context.Context, app state.App, deployment state.Deployment) (state.RuntimeScalingState, error) {
	scaling, err := e.store.RuntimeScalingStateForDeployment(ctx, app.AccountID, app.ID, deployment.ID)
	if err != nil {
		return state.RuntimeScalingState{}, err
	}
	if scaling.AccountID != app.AccountID || scaling.AppID != app.ID || scaling.DeploymentID != deployment.ID || scaling.Scope != normalizedDeploymentScope(deployment.Scope) {
		return state.RuntimeScalingState{}, state.ErrConflict
	}
	return scaling, nil
}

// A compatibility instance without a deployment can stamp production. All
// modern instances derive their scaling lifetime from their actual deployment.
func (l *Loop) stampReaperScaleIn(ctx context.Context, instance InstanceInfo) {
	var err error
	if instance.DeploymentID != "" {
		err = l.engine.Store().StampDeploymentScaleIn(ctx, instance.DeploymentID)
	} else if reaperProductionScope(instance.Scope) {
		err = l.engine.Store().StampAppScaleIn(ctx, instance.AppID)
	}
	if err != nil {
		l.log.Warn("reaper: stamp original environment scale-in", "app", instance.AppID, "deployment", instance.DeploymentID, "err", err)
	}
}
