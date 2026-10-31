package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// environmentBranchDeployment reports whether an environment branch created
// this deployment from its parent's artifact (ADR-20261031193635). Stores
// without branch support never created one.
func (e *Engine) environmentBranchDeployment(ctx context.Context, deploymentID string) (bool, error) {
	reader, ok := e.store.(state.EnvironmentBranchDeploymentReader)
	if !ok {
		return false, nil
	}
	return reader.EnvironmentBranchDeployment(ctx, deploymentID)
}
