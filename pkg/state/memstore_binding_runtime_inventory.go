package state

import "context"

var _ BindingRuntimeInventoryStore = (*MemStore)(nil)

func (m *MemStore) ReadBindingRuntimeInventory(_ context.Context, accountID, appID, scope string) (BindingRuntimeInventory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID {
		return BindingRuntimeInventory{}, ErrNotFound
	}
	result := BindingRuntimeInventory{Deployments: []BindingRuntimeDeployment{}}
	if stamp, exists := m.runtimeConfigChangedAt[appID]; exists {
		result.ChangedAt = cloneHealthTime(&stamp)
	}
	for _, deployment := range m.deployments {
		deploymentScope := deployment.Scope
		if deploymentScope == "" {
			deploymentScope = "default"
		}
		if deployment.AppID != appID || scope != "" && scope != deploymentScope {
			continue
		}
		row := BindingRuntimeDeployment{ID: deployment.ID, Scope: deploymentScope, DeploymentStatus: string(deployment.Status)}
		for _, instance := range m.instances {
			if instance.AppID == appID && instance.DeploymentID == deployment.ID &&
				(instance.Kind == "" || instance.Kind == "wake") && instance.Mode != string(InstanceModeMirror) && State(instance.State).CountsForRAM() {
				addBindingRuntimeInstance(&row, result.ChangedAt, instance)
			}
		}
		if deployment.Status == DeployLive || row.Resident.Current+row.Resident.Stale+row.Resident.Unknown > 0 {
			result.Deployments = append(result.Deployments, row)
		}
	}
	sortBindingRuntimeDeployments(result.Deployments)
	return result, nil
}

// MemStore has no notification outbox. Absence is surfaced as not_queued,
// rather than inferring a successful restart from configuration or task data.
func (m *MemStore) ListBindingRefreshInventory(_ context.Context, accountID, appID string, _ []string) ([]BindingRefreshInventory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if app, ok := m.apps[appID]; !ok || app.AccountID != accountID {
		return nil, ErrNotFound
	}
	return []BindingRefreshInventory{}, nil
}
