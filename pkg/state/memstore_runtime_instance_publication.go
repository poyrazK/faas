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
	instance.Netns, instance.HostIP, instance.GuestUID = p.Netns, p.HostIP, p.GuestUID
	instance.StartedAt, instance.State = time.Now().UTC(), p.targetState()
	m.instances[p.InstanceID] = instance
	return instance, nil
}
