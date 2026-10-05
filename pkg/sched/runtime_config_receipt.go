package sched

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func addRuntimeSecretVersions(inputs *state.RuntimeConfigInputs, candidates []state.AppSecretDeliveryCandidate, all bool) {
	inputs.AllSecrets = all
	for _, candidate := range candidates {
		inputs.SecretVersions[candidate.Scope+"/"+candidate.Key] = candidate.Version
	}
}

func addRuntimeSidecarSecretVersions(inputs *state.RuntimeConfigInputs, candidates []state.AppSecretDeliveryCandidate) {
	inputs.SidecarSecretVersions = make(map[string]int64, len(candidates))
	for _, candidate := range candidates {
		inputs.SidecarSecretVersions[candidate.Scope+"/"+candidate.Key] = candidate.Version
	}
}

func (e *Engine) snapshotRuntimeConfigFresh(ctx context.Context, snap state.Snapshot) (bool, error) {
	receipts, ok := e.store.(state.RuntimeConfigReceiptStore)
	if !ok {
		return true, nil
	}
	deployment, err := e.store.DeploymentByID(ctx, snap.DeploymentID)
	if err != nil {
		return false, err
	}
	inputs, exists, err := receipts.SnapshotRuntimeConfigReceipt(ctx, snap.ID)
	if err != nil {
		return false, err
	}
	if exists {
		if inputs.Scope != normalizedDeploymentScope(deployment.Scope) {
			return false, nil
		}
		return receipts.RuntimeConfigInputsFresh(ctx, deployment.AppID, inputs)
	}
	required, err := receipts.RuntimeConfigReceiptRequired(ctx, deployment.AppID, deployment.Scope)
	return !required, err
}

func (e *Engine) runtimeConfigReceiptStale(ctx context.Context, instance state.Instance, boundary time.Time, scope string) bool {
	receipts, ok := e.store.(state.RuntimeConfigReceiptStore)
	if !ok {
		return runtimeConfigInstanceStale(instance, boundary)
	}
	inputs, exists, err := receipts.InstanceRuntimeConfigReceipt(ctx, instance.ID)
	if err != nil {
		return true
	}
	if exists {
		fresh, err := receipts.RuntimeConfigInputsFresh(ctx, instance.AppID, inputs)
		return err != nil || inputs.Scope != normalizedDeploymentScope(scope) || inputs.Boundary.Before(boundary) || !fresh
	}
	// A newly admitted boot is not yet eligible for an ACK, and the refresh
	// loop must let it reach readiness before deciding whether to replace it.
	if instance.State == string(state.StateWaking) || instance.State == string(state.StateColdBooting) {
		return runtimeConfigInstanceStale(instance, boundary)
	}
	required, err := receipts.RuntimeConfigReceiptRequired(ctx, instance.AppID, scope)
	return err != nil || required || runtimeConfigInstanceStale(instance, boundary)
}
