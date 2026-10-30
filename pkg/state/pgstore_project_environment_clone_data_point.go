package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrProjectEnvironmentCloneSourceChanged means the live source no longer
// matches the clone's frozen configuration capture, so data read now would not
// belong to the captured workloads.
var ErrProjectEnvironmentCloneSourceChanged = fmt.Errorf("clone source changed since it was captured: %w", ErrConflict)

// ProjectEnvironmentCloneDataPointStore reads the instant a clone restores its
// managed databases at (ADR-20261024224230). The capture worker then checks
// that the live source still matches the frozen configuration, in a snapshot
// taken after that instant, so the data and the captured workloads belong to
// the same source revision.
type ProjectEnvironmentCloneDataPointStore interface {
	ProjectEnvironmentCloneDataPointForLease(context.Context, ProjectEnvironmentCloneLease) (time.Time, error)
}

// ProjectEnvironmentCloneDataPointForLease returns the control-plane clock
// under the capturing worker's lease. It is read after the configuration
// capture committed, so the point never precedes the captured source.
func (s *PgStore) ProjectEnvironmentCloneDataPointForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) (time.Time, error) {
	if !validCloneLeaseIdentity(lease) || lease.Operation.Status != CloneOperationCapturing {
		return time.Time{}, ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return time.Time{}, err
	}
	if op.Status != CloneOperationCapturing {
		return time.Time{}, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return time.Time{}, err
	}
	now, err := sqlc.New().ReadProjectEnvironmentCloneClock(ctx, tx)
	if err != nil {
		return time.Time{}, mapErr(err)
	}
	if !now.Valid {
		return time.Time{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, mapErr(err)
	}
	return now.Time.UTC().Truncate(time.Microsecond), nil
}

var _ ProjectEnvironmentCloneDataPointStore = (*PgStore)(nil)
