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

// snapshotRuntimeConfigFreshFor checks snap with its deployment in hand (nil
// reads it). Whether a receipt is required matters only when snap has none,
// so it is read only then. ADR-1741 read both concurrently to save the missing
// receipt's round trip, but environment_runtime_receipt_required is one of the
// wake's heavier statements (7-26 ms on production-us's control plane,
// 2026-10-21) and a restored snapshot almost always carries its receipt: the
// concurrent read held every such wake for an answer it then discarded.
func (e *Engine) snapshotRuntimeConfigFreshFor(ctx context.Context, snap state.Snapshot, dep *state.Deployment, receipt *snapshotReceipt) (bool, error) {
	receipts, ok := e.store.(state.RuntimeConfigReceiptStore)
	if !ok {
		return true, nil
	}
	var deployment state.Deployment
	if dep != nil && dep.ID == snap.DeploymentID {
		deployment = *dep
	} else {
		var err error
		if deployment, err = e.store.DeploymentByID(ctx, snap.DeploymentID); err != nil {
			return false, err
		}
	}
	inputs, exists, err := receipts.SnapshotRuntimeConfigReceipt(ctx, snap.ID)
	if err != nil {
		return false, err
	}
	if receipt != nil {
		*receipt = snapshotReceipt{snapshotID: snap.ID, inputs: inputs, exists: exists}
	}
	if exists {
		if inputs.Scope != normalizedDeploymentScope(deployment.Scope) {
			return false, nil
		}
		app, err := e.store.AppByID(ctx, deployment.AppID)
		if err != nil {
			return false, err
		}
		if handled, fresh, err := state.ReleaseSecretRuntimeInputsFresh(ctx, e.store, app.AccountID, app.ID, deployment.ID, inputs); err != nil || handled {
			return fresh, err
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
		app, err := e.store.AppByID(ctx, instance.AppID)
		if err != nil {
			return true
		}
		if handled, fresh, err := state.ReleaseSecretRuntimeInputsFresh(ctx, e.store, app.AccountID, app.ID, instance.DeploymentID, inputs); err != nil || handled {
			return err != nil || inputs.Boundary.Before(boundary) || !fresh
		}
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
