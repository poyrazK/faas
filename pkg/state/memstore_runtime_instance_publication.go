package state

import (
	"context"
	"time"
)

func (m *MemStore) PublishOwnedInstanceRuntime(_ context.Context, p RuntimeInstancePublication) (Instance, error) {
	if err := validateRuntimeInstancePublication(p); err != nil {
		return Instance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.runtimeAppSecretFenceLocked(p.AccountID, p.AppID, p.InstanceID, p.Fence, true); err != nil {
		return Instance{}, err
	}
	instance := m.instances[p.InstanceID]
	if err := validateWorkerInstanceMutation(instance, p.targetState(), instance.Mode); err != nil {
		return Instance{}, err
	}
	if err := m.requireInstanceLayerArtifactsLocked(instance.DeploymentID, p.targetState()); err != nil {
		return Instance{}, err
	}
	if instance.State != p.ExpectedState || instance.WakeID != p.WakeID || instance.NodeID != p.NodeID {
		return Instance{}, ErrConflict
	}
	snapshot, err := m.runtimeAppValuesLocked(p.AccountID, p.AppID, instance.DeploymentID)
	if err != nil {
		return Instance{}, runtimeSecretFenceError(err)
	}
	current, err := NewRuntimeAppConfigFence(snapshot)
	if err != nil || current != p.ConfigFence || current.SecretFence != p.Fence {
		return Instance{}, ErrConflict
	}
	if p.ExpectedState == string(StateWarm) {
		proof, ok := m.runtimeInstanceConfigProofs[p.InstanceID]
		if !ok || proof.WakeID != p.WakeID || proof.NodeID != p.NodeID || proof.Fence != p.ConfigFence {
			return Instance{}, ErrConflict
		}
	}
	acceptance, err := m.prepareRuntimeUpgradeAcceptanceLocked(p)
	if err != nil {
		return Instance{}, err
	}
	prior := instance
	instance.Netns, instance.HostIP, instance.GuestUID = p.Netns, p.HostIP, p.GuestUID
	instance.StartedAt, instance.State = time.Now().UTC(), p.targetState()
	if err := m.exclusiveRuntimeTransitionLocked(m.instances[p.InstanceID], instance); err != nil {
		return Instance{}, err
	}
	m.instances[p.InstanceID] = instance
	if p.Inputs != nil && p.targetState() == string(StateRunning) {
		if err := m.recordInstanceRuntimeConfigReceiptLocked(p.InstanceID, p.WakeID, *p.Inputs); err != nil {
			m.instances[p.InstanceID] = prior
			return Instance{}, err
		}
	}
	m.runtimeInstanceConfigProofs[p.InstanceID] = runtimeInstanceConfigProof{WakeID: p.WakeID, NodeID: p.NodeID, Fence: p.ConfigFence}
	if acceptance != nil {
		if m.runtimeUpgradeAcceptances == nil {
			m.runtimeUpgradeAcceptances = make(map[string]RuntimeUpgradeAcceptance)
		}
		m.runtimeUpgradeAcceptances[acceptance.DeploymentID] = *acceptance
	}
	return instance, nil
}

type runtimeInstanceConfigProof struct {
	WakeID, NodeID string
	Fence          RuntimeAppConfigFence
}

func (m *MemStore) InstanceRuntimeConfigFence(_ context.Context, accountID, appID, instanceID string) (RuntimeAppConfigFence, error) {
	if err := validateRuntimeAppEnvIDs(accountID, appID, instanceID); err != nil {
		return RuntimeAppConfigFence{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	app, appOK := m.apps[appID]
	instance, ok := m.instances[instanceID]
	proof, proved := m.runtimeInstanceConfigProofs[instanceID]
	if !appOK || app.AccountID != accountID || app.Status == AppDeleted || !ok || instance.AppID != appID || !proved ||
		proof.WakeID != instance.WakeID || proof.NodeID != instance.NodeID || proof.Fence.SecretFence.DeploymentID != instance.DeploymentID {
		return RuntimeAppConfigFence{}, ErrNotFound
	}
	return proof.Fence, nil
}
