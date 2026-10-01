package state

import (
	"context"
	"time"
)

var _ RuntimeConfigMigrationStore = (*MemStore)(nil)

func (m *MemStore) MigrateInstanceOwnerWithRuntimeConfig(_ context.Context, id, from, to, leaseToken string, input RuntimeConfigMigration) error {
	if err := validateRuntimeConfigMigration(input); err != nil {
		return err
	}
	if id == "" || from == "" || to == "" || leaseToken == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	instance, exists := m.instances[id]
	if !exists {
		return ErrNotFound
	}
	if instance.State != string(StateMigrating) || instance.NodeID != from || instance.LeaseToken != leaseToken || instance.WakeID != input.ExpectedWakeID {
		return ErrConflict
	}
	if input.Inputs != nil && normalizedDeploymentScope(m.deployments[instance.DeploymentID].Scope) != input.Inputs.Scope {
		return ErrConflict
	}
	if err := m.checkNodeReservationLocked(to, string(StateRunning), instance.RAMMB); err != nil {
		return err
	}
	now := time.Now().UTC()
	instance.NodeID, instance.WakeID, instance.StartedAt, instance.State = to, input.WakeID, now, string(StateRunning)
	instance.MigratedFromNodeID, instance.MigratedAt, instance.LeaseToken, instance.MigrationStartedAt = &from, &now, leaseToken, nil
	if input.Netns != "" {
		instance.Netns = input.Netns
	}
	if input.HostIP != "" {
		instance.HostIP = input.HostIP
	}
	if input.GuestUID > 0 {
		instance.GuestUID = input.GuestUID
	}
	m.instances[id] = instance
	delete(m.instanceRuntimeConfigReceipts, id)
	if input.Inputs != nil {
		// The new wake and matching deployment were checked before mutation.
		if m.instanceRuntimeConfigReceipts == nil {
			m.instanceRuntimeConfigReceipts = map[string]instanceRuntimeConfigReceipt{}
		}
		m.instanceRuntimeConfigReceipts[id] = instanceRuntimeConfigReceipt{WakeID: input.WakeID, Inputs: cloneRuntimeConfigInputs(*input.Inputs)}
	}
	app := m.apps[instance.AppID]
	app.MigratedAt = &now
	m.apps[app.ID] = app
	return nil
}
