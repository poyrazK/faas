package state

import "context"

var _ ProjectEnvironmentQueueConsumerStore = (*MemStore)(nil)

func (m *MemStore) PrepareProjectEnvironmentQueueConsumers(_ context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentQueueRuntimeSet, error) {
	return m.projectEnvironmentQueueConsumers(accountID, projectID, deploymentID, true)
}

func (m *MemStore) ProjectEnvironmentQueueConsumersForDeployment(_ context.Context, accountID, projectID, deploymentID string) (ProjectEnvironmentQueueRuntimeSet, error) {
	return m.projectEnvironmentQueueConsumers(accountID, projectID, deploymentID, false)
}

func (m *MemStore) projectEnvironmentQueueConsumers(accountID, projectID, deploymentID string, prepare bool) (ProjectEnvironmentQueueRuntimeSet, error) {
	if err := validateQueuePreparationIDs(accountID, projectID, deploymentID); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.Status != DeployLive || !invocationStageScope(workloadEnvironmentSlug(dep.Scope)) {
		return ProjectEnvironmentQueueRuntimeSet{}, ErrNotFound
	}
	env, err := m.workloadSpecEnvironmentLocked(accountID, projectID, workloadEnvironmentSlug(dep.Scope), dep.AppID)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, ErrNotFound
	}
	spec, found := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[deploymentID]]
	if !found {
		return ProjectEnvironmentQueueRuntimeSet{}, ErrProjectEnvironmentQueueCollectionUnavailable
	}
	if spec.EnvironmentID != env.ID || spec.AccountID != accountID || spec.ProjectID != projectID || spec.AppID != dep.AppID || spec.EnvironmentSlug != env.Slug {
		return ProjectEnvironmentQueueRuntimeSet{}, ErrConflict
	}
	book, err := queuePreparationBook(spec)
	if err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	set, exists := m.projectEnvironmentQueueRuntimeSets[deploymentID]
	if !exists {
		if !prepare {
			return ProjectEnvironmentQueueRuntimeSet{}, ErrProjectEnvironmentQueuePreparationUnavailable
		}
		set = newQueueRuntimeSet(spec, deploymentID, book)
		m.projectEnvironmentQueueRuntimeSets[deploymentID] = cloneQueueRuntimeSet(set)
	}
	if err := validateQueueRuntimeSet(set, spec, deploymentID, book); err != nil {
		return ProjectEnvironmentQueueRuntimeSet{}, err
	}
	return cloneQueueRuntimeSet(set), nil
}
