package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

type cloneConfigurationFenceWorkerStore interface {
	state.ProjectEnvironmentCloneWorkerLeaseStore
	state.ProjectEnvironmentCloneConfigurationFenceStore
}

func cloneConfigurationFenceMatches(op state.ProjectEnvironmentCloneOperation, fence state.ProjectEnvironmentCloneConfigurationFence) bool {
	return fence.OperationID == op.ID && fence.AccountID == op.AccountID && fence.ProjectID == op.ProjectID &&
		fence.SourceEnvironment == op.SourceEnvironment && fence.SourceRevisionHash == op.SourceRevisionHash && fence.Generation >= 2 && !fence.HeldAt.IsZero()
}

// This private entry retains configuration stability while the existing
// all-source driver closes instrumented writers. It selects no common point
// and cannot release a successful capture's configuration or data holds.
func (s *server) prepareProjectEnvironmentCloneStableCaptureBarriers(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, cloneCaptureBarrierObservation, error) {
	var zero cloneCaptureBarrierObservation
	store, ok := s.store.(cloneConfigurationFenceWorkerStore)
	if !ok || s.cloneWorkerAdmission == nil {
		return lease, zero, errCloneCheckpointUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCapturing)
	if err != nil {
		return lease, zero, err
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, zero, err
	}
	fence, err := store.AcquireProjectEnvironmentCloneConfigurationFence(ctx, lease)
	if err != nil {
		return lease, zero, err
	}
	if !cloneConfigurationFenceMatches(lease.Operation, fence) {
		return lease, zero, state.ErrConflict
	}
	var out cloneCaptureBarrierObservation
	lease, out, err = s.prepareProjectEnvironmentCloneCaptureBarriers(ctx, lease)
	if err != nil {
		return lease, zero, err
	}
	current, err := store.ProjectEnvironmentCloneConfigurationFenceForLease(ctx, lease)
	if err != nil {
		return lease, zero, err
	}
	if !cloneConfigurationFenceMatches(lease.Operation, current) || current.Generation != fence.Generation || !current.HeldAt.Equal(fence.HeldAt) ||
		out.configuration.Hash != fence.SourceRevisionHash {
		return lease, zero, state.ErrConflict
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, zero, err
	}
	if err := ctx.Err(); err != nil {
		return lease, zero, err
	}
	out.configurationFence = current
	return lease, out, nil
}

// Aborted capture no longer needs configuration stability. Data holds and
// dispatched retirement recovery remain independent compensation gates.
func (s *server) abandonProjectEnvironmentCloneConfigurationFence(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(cloneConfigurationFenceWorkerStore)
	if !ok {
		return lease, errCloneCompensationUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, store, lease, state.CloneOperationCompensating)
	if err != nil {
		return lease, err
	}
	return lease, store.AbandonProjectEnvironmentCloneConfigurationFence(ctx, lease)
}
