package state

import (
	"context"
	"slices"
)

func (m *MemStore) ClaimServiceRolloutReplicaDrains(ctx context.Context, id string, percent int) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, _, err := m.serviceRolloutTargetLocked(id)
	if err != nil {
		return target, err
	}
	if !serviceRolloutDrainClaimValid(target, percent) {
		return target, ErrServiceRolloutInvalid
	}
	if len(target.ServiceRolloutHandoff.RetiringInstanceIDs) > 0 {
		return target, nil
	}

	newReady, desired, err := m.serviceRolloutReadyReplicasLocked(target)
	if err != nil {
		return target, err
	}
	if len(newReady) < ServiceRolloutReadyTarget(desired, percent) {
		return target, ErrServiceRolloutNotReady
	}
	previous := m.deployments[target.ServiceRolloutHandoff.PredecessorDeploymentID]
	if previous.Status != DeployLive || previous.AppID != target.AppID || normalizedDeploymentScope(previous.Scope) != normalizedDeploymentScope(target.Scope) {
		return target, ErrServiceRolloutInvalid
	}
	oldReady, oldDesired, err := m.serviceRolloutReadyReplicasLocked(previous)
	if err != nil {
		return target, err
	}
	if len(oldReady) < ServiceRolloutPredecessorTarget(oldDesired, percent) {
		return target, ErrServiceRolloutNotReady
	}
	ids := selectServiceRolloutDrains(oldReady, oldDesired, percent)
	for _, id := range ids {
		ins := m.instances[id]
		ins.State = string(StateDraining)
		m.instances[id] = ins
	}
	target.ServiceRolloutHandoff.RetiringInstanceIDs = ids
	m.putDeploymentLocked(target.ID, target)
	return target, nil
}

func (m *MemStore) CompleteServiceRolloutReplicaDrains(_ context.Context, id string, expected []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, _, err := m.serviceRolloutTargetLocked(id)
	if err != nil {
		return err
	}
	if !slices.Equal(expected, target.ServiceRolloutHandoff.RetiringInstanceIDs) {
		return ErrServiceRolloutInvalid
	}
	for _, id := range expected {
		if ins, ok := m.instances[id]; ok && State(ins.State) != StateStopped {
			return ErrServiceRolloutInvalid
		}
	}
	target.ServiceRolloutHandoff.RetiringInstanceIDs = nil
	m.putDeploymentLocked(target.ID, target)
	return nil
}

func (m *MemStore) serviceRolloutReadyReplicasLocked(dep Deployment) ([]string, int, error) {
	app, err := m.serviceAppForDeploymentLocked(m.apps[dep.AppID], dep)
	if err != nil {
		return nil, 0, err
	}
	sources, err := DeploymentReadinessSources(dep)
	if err != nil {
		return nil, 0, err
	}
	var replicas []Instance
	for _, ins := range m.instances {
		if ins.DeploymentID == dep.ID {
			replicas = append(replicas, ins)
		}
	}
	targets := serviceReadinessTargets(replicas)
	signals := m.latestInstanceReadinessForTargetsLocked(targets)
	var ids []string
	for _, ins := range targets {
		if serviceReplicaReady(sources, signals[ins.InstanceID]) {
			ids = append(ids, ins.InstanceID)
		}
	}
	desired := 1
	if app.Manifest.ServiceReplicas != nil {
		desired = app.Manifest.ServiceReplicas.Desired
	}
	return ids, desired, nil
}
