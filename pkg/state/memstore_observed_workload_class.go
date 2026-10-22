package state

// adr: 051 — characterization boot: observed workload classification.

import "context"

// RecordObservedWorkloadClass mirrors PgStore.RecordObservedWorkloadClass.
func (m *MemStore) RecordObservedWorkloadClass(_ context.Context, accountID, appID, instanceID string, class WorkloadClass) (bool, error) {
	if class == "" {
		return false, ErrInvalidArgument
	}
	if err := validateRuntimeAppEnvIDs(accountID, appID, instanceID); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return false, ErrNotFound
	}
	proof, proved := m.runtimeInstanceConfigProofs[instanceID]
	instance, found := m.instances[instanceID]
	current := false
	if proved && found && instance.AppID == appID && proof.WakeID == instance.WakeID && proof.NodeID == instance.NodeID &&
		proof.Fence.SecretFence.DeploymentID == instance.DeploymentID {
		if snapshot, err := m.runtimeAppValuesLocked(accountID, appID, instance.DeploymentID); err == nil {
			before, err := NewRuntimeAppConfigFence(snapshot)
			current = err == nil && before == proof.Fence
		}
	}
	if app.WorkloadClass != class {
		app.ScalingPolicyRevision++
	}
	app.WorkloadClass = class
	m.apps[appID] = app
	if !current {
		return false, nil
	}
	snapshot, err := m.runtimeAppValuesLocked(accountID, appID, instance.DeploymentID)
	if err != nil {
		return false, nil
	}
	next, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || next.SecretFence != proof.Fence.SecretFence || next.Fingerprint == proof.Fence.Fingerprint {
		return false, nil
	}
	proof.Fence = next
	m.runtimeInstanceConfigProofs[instanceID] = proof
	return true, nil
}
