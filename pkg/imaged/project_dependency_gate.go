package imaged

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) gateProjectDependencyActivation(ctx context.Context, dep state.Deployment) (bool, error) {
	store, ok := h.store.(state.DeploymentDependencyGateStore)
	if !ok {
		app, err := state.AppForDeployment(ctx, h.store, dep)
		if err != nil {
			return false, err
		}
		for _, condition := range app.Manifest.ProjectDependencyConditions {
			if condition == api.ComposeDependencyHealthy {
				return false, errors.New("imaged: store does not support project dependency release gates")
			}
		}
		return false, nil
	}
	now := time.Now().UTC()
	if h.dependencyGateNow != nil {
		now = h.dependencyGateNow().UTC()
	}
	_, err := store.CheckDeploymentDependencies(ctx, dep.ID, now)
	if err == nil {
		return false, nil
	}
	if stop, gateErr := h.handleProjectDependencyBlocker(ctx, dep, err); stop {
		return true, gateErr
	}
	return false, fmt.Errorf("imaged: check project dependency gate: %w", err)
}

func (h *Handler) handleProjectDependencyBlocker(ctx context.Context, dep state.Deployment, err error) (bool, error) {
	var blocker *state.DependencyGateError
	if !errors.As(err, &blocker) {
		return false, nil
	}
	if blocker.Pending() {
		return true, &db.DeferredNotificationError{Cause: blocker, Delay: api.ProjectDependencyGatePoll}
	}
	if _, err := h.store.SetDeploymentFailed(ctx, dep.ID, blocker.Code, blocker.Detail); err != nil {
		return true, fmt.Errorf("imaged: fail blocked dependency release: %w", err)
	}
	h.notifyDeploymentState(ctx, dep.AppID, dep.ID, state.DeployFailed)
	return true, nil
}
