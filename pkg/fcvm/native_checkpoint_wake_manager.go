// adr: 590
package fcvm

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/state"
)

// NativeCheckpointWakeOwnership is incoming journal evidence only. It contains
// no launch permit, cleanup authority or closure receipt. NativeGeneration
// denotes a planned binding, never proof of physical publication or exit.
type NativeCheckpointWakeOwnership struct {
	Dispatch                                           state.ProjectEnvironmentCloneWakeDispatch
	IncomingGeneration, KernelBootID, NativeGeneration string
	Claimed, Revoked                                   bool
}

// The existing native qualification node configuration is the local compute
// identity; neither this surface nor that configuration enables serving launch.
func (m *Manager) checkpointWakeJournal(frame state.ProjectEnvironmentCloneWakeDispatch) (*nativeCheckpointWakeJournal, error) {
	if m == nil || !nativeQualificationUUID(m.nativeQualificationNodeID) {
		return nil, fmt.Errorf("native checkpoint wake: local node is unavailable: %w", state.ErrConflict)
	}
	if err := validateCheckpointWakeFrame(frame, m.nativeQualificationNodeID); err != nil {
		return nil, err
	}
	v := m.nativeVMM()
	if v == nil {
		return nil, fmt.Errorf("native checkpoint wake: journal-backed owner is unavailable: %w", state.ErrConflict)
	}
	return v.nativeRecoveryRuntime().journal.checkpointWakes(m.nativeQualificationNodeID), nil
}

// ClaimCheckpointWakeDispatch persists original incoming ownership before any
// physical preparation. Its result deliberately cannot authorize a launch. The
// trusted caller must supply the retained dispatch, never tracing metadata.
func (m *Manager) ClaimCheckpointWakeDispatch(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch) (NativeCheckpointWakeOwnership, error) {
	return m.updateCheckpointWakeDispatch(ctx, frame, true)
}

// RevokeCheckpointWakeDispatch persists an incoming tombstone only. It does not
// retire a process, revoke physical resources or grant an evidence-release proof.
func (m *Manager) RevokeCheckpointWakeDispatch(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch) (NativeCheckpointWakeOwnership, error) {
	return m.updateCheckpointWakeDispatch(ctx, frame, false)
}

func (m *Manager) updateCheckpointWakeDispatch(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, claim bool) (NativeCheckpointWakeOwnership, error) {
	j, err := m.checkpointWakeJournal(frame)
	if err != nil {
		return NativeCheckpointWakeOwnership{}, err
	}
	var record nativeCheckpointWakeRecord
	if claim {
		record, err = j.claim(ctx, frame)
	} else {
		record, err = j.revoke(ctx, frame)
	}
	if err != nil {
		return NativeCheckpointWakeOwnership{}, fmt.Errorf("native checkpoint wake: retain original ownership: %w", err)
	}
	return NativeCheckpointWakeOwnership{Dispatch: record.Dispatch, IncomingGeneration: record.Generation, KernelBootID: record.KernelBootID, NativeGeneration: record.NativeGeneration, Claimed: record.Claimed, Revoked: record.Revoked}, nil
}

// PrepareCheckpointWakeLaunch durably binds the original dispatch to a planned
// physical generation before publishing its prepared record. A bound generation
// grants no launch authority; tagged serving and producers remain gated.
func (m *Manager) PrepareCheckpointWakeLaunch(ctx context.Context, frame state.ProjectEnvironmentCloneWakeDispatch, lease Lease) (NativeCheckpointWakeOwnership, error) {
	j, err := m.checkpointWakeJournal(frame)
	if err != nil {
		return NativeCheckpointWakeOwnership{}, err
	}
	record, err := j.preparePhysical(ctx, frame, lease)
	if err != nil {
		return NativeCheckpointWakeOwnership{}, fmt.Errorf("native checkpoint wake: prepare original physical binding: %w", err)
	}
	return NativeCheckpointWakeOwnership{Dispatch: record.Dispatch, IncomingGeneration: record.Generation, KernelBootID: record.KernelBootID, NativeGeneration: record.NativeGeneration, Claimed: record.Claimed, Revoked: record.Revoked}, nil
}
