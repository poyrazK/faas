package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// A clone that has not published within its deadline is abandoned and its
// target removed. Target deployments reuse the source artifacts, so a
// data-less clone only waits for their prime; restoring managed databases may
// take longer. A stuck clone should not hold the target slug forever.
const (
	projectEnvironmentCloneDatalessDeadline = 30 * time.Minute
	projectEnvironmentCloneDatabaseDeadline = 2 * time.Hour
)

// Managed databases are restored only once their data point is this old, so
// the provider's clock and write-ahead log have passed it
// (ADR-20261024224230).
const projectEnvironmentCloneDataPointSettle = 30 * time.Second

var (
	errCloneTargetDeploymentFailed = state.ErrProjectEnvironmentCloneTargetDeploymentEnded
	errCloneTargetDatabaseFailed   = fmt.Errorf("clone target database restore failed: %w", state.ErrConflict)
)

// directCloneFailure names why a clone whose target can be deleted directly
// cannot finish, or returns "" while it still can (ADR-20261024123122,
// ADR-20261024224230). Transient errors keep retrying until the deadline.
func directCloneFailure(op state.ProjectEnvironmentCloneOperation, stepErr error, now time.Time) string {
	if !state.ProjectEnvironmentCloneIsDirectlyCompensable(op) {
		return ""
	}
	deadline := projectEnvironmentCloneDatalessDeadline
	if state.ProjectEnvironmentCloneHasManagedDatabases(op) {
		deadline = projectEnvironmentCloneDatabaseDeadline
	}
	switch {
	case errors.Is(stepErr, errCloneTargetDeploymentFailed):
		return state.CloneFailureTargetDeployment
	case errors.Is(stepErr, errCloneTargetDatabaseFailed):
		return state.CloneFailureTargetDatabase
	case errors.Is(stepErr, state.ErrProjectEnvironmentCloneSourceChanged):
		return state.CloneFailureSourceChanged
	case !op.CreatedAt.IsZero() && now.Sub(op.CreatedAt) > deadline:
		return state.CloneFailureDeadline
	}
	return ""
}

// failDirectProjectEnvironmentClone moves the operation straight to
// compensating with its failure code; the next pass removes the target.
func failDirectProjectEnvironmentClone(ctx context.Context, store state.ProjectEnvironmentCloneOperationStore, lease state.ProjectEnvironmentCloneLease, code string) (state.ProjectEnvironmentCloneLease, error) {
	op := lease.Operation
	failed, err := store.AdvanceProjectEnvironmentCloneOperation(ctx, op.AccountID, op.ProjectID, op.ID, op.Status, state.CloneOperationCompensating, op.Revision, op.Resources, code)
	if err != nil {
		return lease, fmt.Errorf("fail clone (%s): %w", code, err)
	}
	lease.Operation = failed
	return lease, nil
}

// compensateDirectProjectEnvironmentClone cancels the target deployments and
// waits for schedd to drain their instances, withdraws the target bindings,
// deletes the databases the clone restored, then removes the target
// environment and marks the operation compensated.
func (s *server) compensateDirectProjectEnvironmentClone(ctx context.Context, lease state.ProjectEnvironmentCloneLease) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneDirectCompensationStore)
	if !ok {
		return lease, errCloneCompensationUnavailable
	}
	active, err := store.RetireProjectEnvironmentCloneTargetsForLease(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("retire clone targets: %w", err)
	}
	if active > 0 {
		return lease, state.ErrProjectEnvironmentCloneTargetsDraining
	}
	databases, err := store.WithdrawProjectEnvironmentCloneDatabaseBindingsForLease(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("withdraw clone database bindings: %w", err)
	}
	if len(databases) > 0 && s.managedPostgres == nil {
		return lease, managedpostgres.ErrUnavailable
	}
	op := lease.Operation
	for _, id := range databases {
		deleteCtx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		_, err := s.managedPostgres.DeleteEnvironmentCloneTarget(deleteCtx, op.AccountID, id, op.ID)
		cancel()
		if err != nil {
			return lease, fmt.Errorf("delete clone target database: %w", err)
		}
	}
	finished, err := store.FinishProjectEnvironmentCloneDirectCompensationForLease(ctx, lease)
	if err != nil {
		return lease, fmt.Errorf("remove clone target: %w", err)
	}
	lease.Operation = finished
	lease.Token, lease.ExpiresAt = "", time.Time{}
	return lease, nil
}
