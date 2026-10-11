package sched

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Pinned upgrades need their own prime. Job artifact notification and ordinary
// restore/recovery paths cannot substitute for candidate cold-boot readiness.
func (e *Engine) runtimeUpgradePrimeTarget(ctx context.Context, app state.App, dep state.Deployment) (*state.RuntimeRelease, error) {
	targets, ok := e.store.(state.RuntimeUpgradeTargetStore)
	if !ok {
		return nil, nil // historical stores cannot supply upgrade preparation
	}
	target, err := targets.DeploymentRuntimeUpgradeTarget(ctx, dep.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sched: prime: runtime upgrade target: %w", err)
	}
	if executionModeForApp(app) == api.ExecutionModeJob || dep.TrafficPercent != 0 || !dep.TrafficPercentExplicit ||
		dep.CanaryTotalSteps != 0 || state.IsServiceRollout(dep) {
		return nil, fmt.Errorf("sched: prime: runtime upgrade needs a held cold-boot candidate: %w", state.ErrConflict)
	}
	baselines, ok := e.store.(state.RuntimeUpgradeBaselineStore)
	if !ok {
		return nil, fmt.Errorf("sched: prime: runtime upgrade baseline unavailable: %w", state.ErrConflict)
	}
	if err := baselines.ValidateDeploymentRuntimeUpgradeBaseline(ctx, dep.ID); err != nil {
		return nil, fmt.Errorf("sched: prime: runtime upgrade baseline: %w", err)
	}
	if err := state.RequireRuntimeReleaseQualification(ctx, e.store, target); err != nil {
		return nil, err
	}
	acceptances, ok := e.store.(state.RuntimeUpgradeAcceptanceStore)
	if !ok {
		return nil, fmt.Errorf("sched: prime: runtime upgrade acceptance unavailable: %w", state.ErrConflict)
	}
	if _, err := acceptances.DeploymentRuntimeUpgradeAcceptance(ctx, dep.ID); !errors.Is(err, state.ErrNotFound) {
		if err == nil {
			err = state.ErrConflict
		}
		return nil, fmt.Errorf("sched: prime: runtime upgrade needs a new candidate attempt: %w", err)
	}
	return &target, nil
}
