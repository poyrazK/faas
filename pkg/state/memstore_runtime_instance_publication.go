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
	instance.Netns, instance.HostIP, instance.GuestUID = p.Netns, p.HostIP, p.GuestUID
	instance.StartedAt, instance.State = time.Now().UTC(), p.targetState()
	m.instances[p.InstanceID] = instance
	m.runtimeInstanceConfigProofs[p.InstanceID] = runtimeInstanceConfigProof{WakeID: p.WakeID, NodeID: p.NodeID, Fence: p.ConfigFence}
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
