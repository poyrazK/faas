package state

func (m *MemStore) runtimeAppSecretFenceLocked(accountID, appID, instanceID string, fence RuntimeAppSecretFence) (string, error) {
	instance, ok := m.instances[instanceID]
	app, appOK := m.apps[appID]
	dep, depOK := m.deployments[instance.DeploymentID]
	if !ok || !appOK || !depOK || instance.AppID != appID || app.AccountID != accountID || app.Status == AppDeleted || dep.AppID != appID || !dep.DeploymentAliasActive() {
		return "", ErrConflict
	}
	scope := normalizedDeploymentScope(dep.Scope)
	if fence.empty() {
		if invocationStageScope(scope) || m.deploymentRuntimeEnvironmentOwners[dep.ID] != "" || m.projectEnvironmentWorkloadDeploymentSpecs[dep.ID] != "" {
			return "", ErrConflict
		}
		return scope, nil
	}
	if fence.DeploymentID != instance.DeploymentID || fence.Scope != scope || !State(instance.State).CountsForRAM() {
		return "", ErrConflict
	}
	snapshot, err := m.runtimeAppValuesLocked(accountID, appID, instance.DeploymentID)
	if err != nil {
		return "", ErrConflict
	}
	current, err := NewRuntimeAppSecretFence(snapshot)
	if err != nil || current != fence {
		return "", ErrConflict
	}
	return scope, nil
}
