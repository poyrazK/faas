package sched

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// Read the stamp before inputs. A transaction committing during preparation
// makes the receipt stale; readiness time must never replace this observation.
func (e *Engine) prepareRuntimeConfigInputs(ctx context.Context, accountID, appID, scope string) (state.RuntimeConfigInputs, []fcvm.APIEnvEntry, error) {
	scope = normalizedDeploymentScope(scope)
	boundary, stamped, err := state.RuntimeConfigChangedAtForScope(ctx, e.store, appID, scope)
	if err != nil {
		return state.RuntimeConfigInputs{}, nil, err
	}
	if !stamped {
		boundary = time.Unix(0, 0).UTC()
	}
	rows, err := e.store.ListAppEnvInScope(ctx, accountID, appID, scope)
	if err != nil {
		return state.RuntimeConfigInputs{}, nil, err
	}
	inputs := state.RuntimeConfigInputs{Scope: scope, Boundary: boundary, Variables: map[string]string{}, SecretVersions: map[string]int64{}}
	entries := make([]fcvm.APIEnvEntry, 0, len(rows))
	for _, row := range rows {
		inputs.Variables[row.Key] = row.Value
		entries = append(entries, fcvm.APIEnvEntry{Key: row.Key, Value: row.Value})
		if row.UpdatedAt.After(inputs.Boundary) {
			inputs.Boundary = row.UpdatedAt
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })
	return inputs, entries, nil
}

func addRuntimeSecretVersions(inputs *state.RuntimeConfigInputs, candidates []state.AppSecretDeliveryCandidate, all bool) {
	inputs.AllSecrets = all
	for _, candidate := range candidates {
		inputs.SecretVersions[candidate.Scope+"/"+candidate.Key] = candidate.Version
	}
}

func (e *Engine) recordRuntimeConfigReceipt(ctx context.Context, instanceID, wakeID string, inputs *state.RuntimeConfigInputs) error {
	if inputs == nil {
		return nil // a legacy restore carries no receipt; managed scopes refuse it
	}
	if receipts, ok := e.store.(state.RuntimeConfigReceiptStore); ok {
		return receipts.RecordInstanceRuntimeConfigReceipt(ctx, instanceID, wakeID, *inputs)
	}
	return nil
}

func (e *Engine) publishRuntimeConfigReceipt(ctx context.Context, id, expectedState, netns, hostIP string, uid int, wakeID string, inputs *state.RuntimeConfigInputs, appID string) (state.Instance, error) {
	if err := e.checkManagedPostgresAdmission(ctx, appID); err != nil {
		return state.Instance{}, err
	}
	if publisher, ok := e.store.(state.RuntimeConfigReceiptPublisher); ok && inputs != nil {
		return publisher.PublishInstanceRuntimeWithConfig(ctx, id, expectedState, netns, hostIP, uid, wakeID, *inputs)
	}
	instance, err := e.store.PublishInstanceRuntime(ctx, id, expectedState, netns, hostIP, uid)
	if err == nil {
		err = e.recordRuntimeConfigReceipt(ctx, id, wakeID, inputs)
	}
	return instance, err
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
