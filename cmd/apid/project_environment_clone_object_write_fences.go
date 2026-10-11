package main

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// Abandoning capture resumes only object admission after dispatched native
// retirements have drained. Unknown requests/native grants remain tracked.
// This does not publish a stage or clear other cleanup.
func (s *server) abandonProjectEnvironmentCloneObjectWriteFences(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneObjectWriteFenceStore)
	leases, leasesOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leasesOK {
		return lease, errCloneCompensationUnavailable
	}
	lease, err := renewCloneObjectWorkerLease(ctx, leases, lease, state.CloneOperationCompensating)
	if err != nil {
		return lease, err
	}
	fences, err := store.ProjectEnvironmentCloneObjectWriteFencesForLease(ctx, lease)
	if err != nil {
		return lease, err
	}
	if len(fences) != 0 {
		lease, err = s.resumeCloneObjectGrantRetirementsForAbandonment(ctx, lease, fences)
		if err != nil {
			return lease, err
		}
	}
	return lease, store.AbandonProjectEnvironmentCloneObjectWriteFences(ctx, lease)
}
