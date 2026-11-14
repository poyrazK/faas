package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// Readiness makes a checked rollback LIVE at zero traffic before its final
// commit. Preserve its qualification worker while the current release serves.
// A hold never starts additional workers or exempts them from the account cap.
func (e *Engine) holdCheckedRollbackWorkers(ctx context.Context, app state.App, deployments []state.Deployment, targets, held map[string]struct{}) error {
	reader, ok := e.store.(state.CheckedRollbackStore)
	if !ok {
		return nil
	}
	for _, dep := range deployments {
		if dep.Status != state.DeployLive || dep.TrafficPercent != 0 {
			continue
		}
		r, err := reader.CheckedRollbackForTarget(ctx, dep.ID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read worker rollback: %w", err)
		}
		if r.Service || r.Status != "ready" && r.Status != "blocked" {
			continue
		}
		if r.AppID != app.ID || r.TargetDeploymentID != dep.ID || r.Scope != normalizedDeploymentScope(dep.Scope) {
			return state.ErrConflict
		}
		deployed, err := state.ResolveAppForDeployment(ctx, e.store, app, dep)
		if err != nil {
			return err
		}
		if instanceModeForApp(deployed) != string(state.InstanceModeWorker) {
			continue
		}
		held[dep.ID] = struct{}{}
		// Project release graphs still own the serving target for project apps.
		if app.ProjectID != "" && app.PreviewOfSlug == "" {
			continue
		}
		currentFound := false
		for _, other := range deployments {
			if normalizedDeploymentScope(other.Scope) != r.Scope {
				continue
			}
			delete(targets, other.ID)
			if other.ID == r.CurrentDeploymentID && other.Status == state.DeployLive && other.TrafficPercent == 100 {
				currentFound = true
			}
		}
		if !currentFound {
			return state.ErrConflict
		}
		targets[r.CurrentDeploymentID] = struct{}{}
	}
	return nil
}
