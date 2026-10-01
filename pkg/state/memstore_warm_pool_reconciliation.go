package state

import (
	"context"
	"sort"
)

func (m *MemStore) WarmPoolReconciliationAppIDs(_ context.Context, nodeID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidates := map[string]bool{}
	for _, instance := range m.instances {
		if instance.State == string(StateWarm) {
			candidates[instance.AppID] = true
		}
	}
	for deploymentID, specID := range m.projectEnvironmentWorkloadDeploymentSpecs {
		deployment, found := m.deployments[deploymentID]
		spec, pinned := m.projectEnvironmentWorkloadSpecs[specID]
		if found && pinned && deployment.Status == DeployLive && spec.AppID == deployment.AppID && spec.Settings.WarmPoolSize > 0 {
			candidates[deployment.AppID] = true
		}
	}
	ids := make([]string, 0, len(candidates))
	for _, app := range m.apps {
		if app.Status != AppDeleted && (nodeID == "" || app.NodeID == nodeID) && (app.WarmPoolSize > 0 || candidates[app.ID]) {
			ids = append(ids, app.ID)
		}
	}
	sort.Strings(ids)
	return ids, nil
}
