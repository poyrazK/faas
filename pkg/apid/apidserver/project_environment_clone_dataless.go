package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// A data-less clone that has not published within this long is abandoned and
// its target removed. Target deployments reuse the source artifacts, so only
// their prime remains; a stuck prime should not hold the target slug forever.
const projectEnvironmentCloneDatalessDeadline = 30 * time.Minute

var errCloneTargetDeploymentFailed = state.ErrProjectEnvironmentCloneTargetDeploymentEnded

// datalessCloneFailure names why a data-less clone in copying or publishing
// cannot finish, or returns "" while it still can (ADR-20261024123122).
// Transient errors keep retrying until the deadline.
func datalessCloneFailure(op state.ProjectEnvironmentCloneOperation, stepErr error, now time.Time) string {
	if !state.ProjectEnvironmentCloneIsDataless(op) {
		return ""
	}
	switch {
	case errors.Is(stepErr, errCloneTargetDeploymentFailed):
		return "target_deployment_failed"
	case !op.CreatedAt.IsZero() && now.Sub(op.CreatedAt) > projectEnvironmentCloneDatalessDeadline:
		return "clone_deadline_exceeded"
	}
	return ""
}

// failDatalessProjectEnvironmentClone moves the operation straight to
// compensating with its failure code; the next pass removes the target.
func failDatalessProjectEnvironmentClone(ctx context.Context, store state.ProjectEnvironmentCloneOperationStore, lease state.ProjectEnvironmentCloneLease, code string) (state.ProjectEnvironmentCloneLease, error) {
	op := lease.Operation
	failed, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, code)
	if err != nil {
		return lease, fmt.Errorf("fail data-less clone (%s): %w", code, err)
	}
	lease.Operation = failed
	return lease, nil
}

// compensateDatalessProjectEnvironmentClone cancels the target deployments,
// waits for schedd to drain their instances, then removes the target
// environment and marks the operation compensated.
func (s *server) compensateDatalessProjectEnvironmentClone(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneDatalessCompensationStore)
	if !ok {
		return lease, errCloneCompensationUnavailable
	}
	active, err := store.RetireDatalessProjectEnvironmentCloneTargetsForLease(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("retire clone targets: %w", err)
	}
	if active > 0 {
		return lease, state.ErrProjectEnvironmentCloneTargetsDraining
	}
	finished, err := store.FinishDatalessProjectEnvironmentCloneCompensationForLease(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("remove clone target: %w", err)
	}
	lease.Operation = finished
	lease.Token, lease.ExpiresAt = "", time.Time{}
	return lease, nil
}
