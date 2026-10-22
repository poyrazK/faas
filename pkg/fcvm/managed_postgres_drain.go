package fcvm

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/state"
)

// Configure before serving. Native retirement also requires the vmmd RPC's
// authoritative database guard; this local identity is not an authorization.
func (m *Manager) WithManagedPostgresDrainNodeID(nodeID string) *Manager {
	m.managedPostgresDrainNodeID = nodeID
	return m
}

// RetireManagedPostgresDrain joins delayed boot/restore before stopping the
// original physical generation. Journal absence and legacy VMMs fail closed.
// The SQL authority must remain locked throughout this call.
func (m *Manager) RetireManagedPostgresDrain(ctx context.Context, frame state.ManagedPostgresDrainFrame) (proof state.ManagedPostgresDrainProof, result error) {
	if !frame.Valid() || frame.NodeID != m.managedPostgresDrainNodeID {
		return proof, state.ErrConflict
	}
	v := m.nativeVMM()
	if v == nil {
		return proof, state.ErrConflict
	}
	if err := m.RecoverNativeProcesses(ctx); err != nil {
		return proof, err
	}
	stop, err := m.beginInstanceStop(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer m.finishInstanceStop(frame.InstanceID, stop)
	if err := m.joinForTeardown(ctx, frame.InstanceID); err != nil {
		return proof, err
	}
	journal := v.nativeRecoveryRuntime().journal
	before, err := journal.read(frame.InstanceID)
	if err != nil {
		return proof, errors.Join(state.ErrConflict, err)
	}
	// Retained cleanup has physical identity but no reconstructed app metadata.
	// SQL supplies its logical authority; only live records have app metadata.
	m.mu.Lock()
	live := m.live[frame.InstanceID]
	qualification := m.qualificationInstances[frame.InstanceID]
	m.mu.Unlock()
	if qualification != nil {
		return proof, state.ErrConflict
	}
	if live != nil &&
		(live.AppID != frame.AppID || live.DeploymentID != frame.DeploymentID || live.nativeGeneration != before.Generation) {
		return proof, state.ErrConflict
	}
	if _, err := m.destroyWithReservation(ctx, ctx, frame.InstanceID, ""); err != nil {
		return proof, err
	}
	lock, err := journal.lock(ctx, frame.InstanceID)
	if err != nil {
		return proof, err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	after, err := journal.read(frame.InstanceID)
	if err != nil {
		return proof, err
	}
	if after.Generation != before.Generation || after.KernelBootID != before.KernelBootID ||
		!sameNativePhysicalLease(after.Lease, before.Lease) || !after.Revoked || !after.ExitConfirmed || !after.ResourcesRemoved {
		return proof, state.ErrConflict
	}
	proof = state.ManagedPostgresDrainProof{DrainID: frame.ID, NativeGeneration: after.Generation, KernelBootID: after.KernelBootID,
		ProcessesExited: true, ResourcesRemoved: true}
	if !proof.ValidFor(frame) {
		return state.ManagedPostgresDrainProof{}, state.ErrConflict
	}
	return proof, nil
}
