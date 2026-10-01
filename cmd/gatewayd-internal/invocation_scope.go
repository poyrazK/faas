package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Recheck the environment after the HTTP hop, including unpinned invocations.
// An older scheduler response must not authorize forwarding across scopes.
func (a *synthAdapter) verifyInvocationScopeTarget(ctx context.Context, appID, scope string, target gateway.Target) error {
	dep, err := a.store.DeploymentByID(ctx, target.DeploymentID)
	if err != nil {
		return fmt.Errorf("gateway synth: resolve target deployment: %w", err)
	}
	depScope := dep.Scope
	if depScope == "" {
		depScope = api.DefaultEnvScope
	}
	if dep.AppID != appID || depScope != scope {
		return fmt.Errorf("%w: pre-woken target does not belong to invocation scope", state.ErrConflict)
	}
	instance, err := a.store.InstanceByID(ctx, target.InstanceID)
	if err != nil || instance.AppID != appID || instance.DeploymentID != dep.ID ||
		instance.NodeID != target.NodeID || instance.State != string(state.StateRunning) ||
		target.WakeID != "" && instance.WakeID != target.WakeID {
		return fmt.Errorf("%w: pre-woken instance does not belong to invocation scope", state.ErrConflict)
	}
	return nil
}
