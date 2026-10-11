package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneConfigurationFenceStore = (*PgStore)(nil)

func cloneConfigurationFenceFromSQL(op ProjectEnvironmentCloneOperation, row sqlc.ProjectEnvironmentCloneConfigurationGuard) (ProjectEnvironmentCloneConfigurationFence, error) {
	if row.State != "held" || pgUUIDString(row.OperationID) != op.ID || pgUUIDString(row.AccountID) != op.AccountID || pgUUIDString(row.ProjectID) != op.ProjectID ||
		row.SourceEnvironment != op.SourceEnvironment || row.SourceRevisionHash != op.SourceRevisionHash || row.Generation < 2 || !row.HeldAt.Valid {
		return ProjectEnvironmentCloneConfigurationFence{}, ErrConflict
	}
	return ProjectEnvironmentCloneConfigurationFence{AccountID: op.AccountID, ProjectID: op.ProjectID, OperationID: op.ID,
		SourceEnvironment: op.SourceEnvironment, SourceRevisionHash: op.SourceRevisionHash, Generation: row.Generation, HeldAt: row.HeldAt.Time}, nil
}

// Acquisition locks the synchronization clock before the operation and guard:
// an admitted project deletion can cascade into that operation. It takes no
// configuration row locks after the clock, since writers can hold those rows
// while waiting for the clock. Plain reads are stable under the held guard.
// Guard ownership changes require the owner operation lock. Read an open or
// foreign guard without row locks: an admitted deletion may already own that
// row while waiting for our operation. Only an owned held guard is released.
func (s *PgStore) cloneConfigurationFenceTx(ctx context.Context, lease ProjectEnvironmentCloneLease, acquiring bool,
	fn func(context.Context, pgx.Tx, ProjectEnvironmentCloneOperation, sqlc.ProjectEnvironmentCloneConfigurationGuard) (ProjectEnvironmentCloneConfigurationFence, error)) (ProjectEnvironmentCloneConfigurationFence, error) {
	var zero ProjectEnvironmentCloneConfigurationFence
	if !validCloneLeaseIdentity(lease) {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if acquiring {
		caller := lease.Operation
		_, err := sqlc.New().AdvanceProjectEnvironmentCloneConfigurationClock(ctx, tx, sqlc.AdvanceProjectEnvironmentCloneConfigurationClockParams{
			OperationID: mustPgUUID(caller.ID), AccountID: mustPgUUID(caller.AccountID), ProjectID: mustPgUUID(caller.ProjectID),
			ExpectedRevision: caller.Revision, WorkerToken: lease.Token, SourceEnvironment: caller.SourceEnvironment, SourceRevisionHash: caller.SourceRevisionHash})
		if errors.Is(err, pgx.ErrNoRows) {
			return zero, ErrConflict
		}
		if err != nil {
			return zero, mapProjectCloneSnapshotErr(err)
		}
	}
	op, err := lockCloneWorkloadOperationRowTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return zero, mapErr(err)
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCompensating {
		return zero, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	if acquiring {
		if op.Status != CloneOperationCapturing || len(op.Resources) != 0 {
			return zero, ErrConflict
		}
	}
	row, err := sqlc.New().ReadProjectEnvironmentCloneConfigurationGuard(ctx, tx, sqlc.ReadProjectEnvironmentCloneConfigurationGuardParams{
		ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID)})
	if err != nil {
		return zero, mapErr(err)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	fence, err := fn(ctx, tx, op, row)
	if err != nil {
		return zero, mapProjectCloneSnapshotErr(err)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, mapProjectCloneSnapshotErr(err)
	}
	return fence, nil
}

func (s *PgStore) AcquireProjectEnvironmentCloneConfigurationFence(ctx context.Context, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationFence, error) {
	return s.cloneConfigurationFenceTx(ctx, lease, true, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, row sqlc.ProjectEnvironmentCloneConfigurationGuard) (ProjectEnvironmentCloneConfigurationFence, error) {
		var zero ProjectEnvironmentCloneConfigurationFence
		if row.State == "held" {
			if _, err := cloneConfigurationFenceFromSQL(op, row); err != nil {
				return zero, err
			}
		} else {
			var err error
			row, err = sqlc.New().HoldProjectEnvironmentCloneConfiguration(ctx, tx, sqlc.HoldProjectEnvironmentCloneConfigurationParams{
				ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID), OperationID: mustPgUUID(op.ID),
				SourceEnvironment: op.SourceEnvironment, SourceRevisionHash: op.SourceRevisionHash, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
			if err != nil {
				return zero, mapErr(err)
			}
		}
		if err := validateGuardedCloneConfigurationTx(ctx, tx, op); err != nil {
			return zero, err
		}
		return cloneConfigurationFenceFromSQL(op, row)
	})
}

func validateGuardedCloneConfigurationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation) error {
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	frozen, err := verifyCloneConfigurationCaptureDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, records)
	if err != nil {
		return err
	}
	if frozen.Version != 1 {
		return ErrProjectEnvironmentCloneBindingCaptureUnavailable
	}
	coverage, err := readCloneSchemaCoverageDB(ctx, tx)
	if err != nil {
		return err
	}
	if err := requireKnownCloneSchema(coverage); err != nil {
		return err
	}
	op.SourceReleaseSetID, err = sqlc.New().ReadProjectEnvironmentCloneSourceRelease(ctx, tx, sqlc.ReadProjectEnvironmentCloneSourceReleaseParams{
		ProjectID: mustPgUUID(op.ProjectID), Environment: op.SourceEnvironment})
	if err != nil {
		return mapErr(err)
	}
	ids, err := sqlc.New().ReadProjectEnvironmentCloneSourceAppIDs(ctx, tx, sqlc.ReadProjectEnvironmentCloneSourceAppIDsParams{
		AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID)})
	if err != nil {
		return mapErr(err)
	}
	scopes, err := projectCloneValueScopesForAppsDB(ctx, tx, ProjectEnvironmentClone{AccountID: op.AccountID, ProjectID: op.ProjectID, SourceSlug: op.SourceEnvironment}, ids)
	if err != nil {
		return err
	}
	liveRecords, err := captureCloneConfigurationWorkloadsForScopesTx(ctx, tx, op, scopes, false)
	if err != nil {
		return err
	}
	live, _, err := cloneConfigurationRoot(op, liveRecords)
	if err != nil {
		return err
	}
	if live != frozen {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ProjectEnvironmentCloneConfigurationFenceForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneConfigurationFence, error) {
	return s.cloneConfigurationFenceTx(ctx, lease, false, func(_ context.Context, _ pgx.Tx, op ProjectEnvironmentCloneOperation, row sqlc.ProjectEnvironmentCloneConfigurationGuard) (ProjectEnvironmentCloneConfigurationFence, error) {
		if row.State == "open" || pgUUIDString(row.OperationID) != op.ID {
			return ProjectEnvironmentCloneConfigurationFence{}, ErrNotFound
		}
		return cloneConfigurationFenceFromSQL(op, row)
	})
}

func (s *PgStore) AbandonProjectEnvironmentCloneConfigurationFence(ctx context.Context, lease ProjectEnvironmentCloneLease) error {
	_, err := s.cloneConfigurationFenceTx(ctx, lease, false, func(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, row sqlc.ProjectEnvironmentCloneConfigurationGuard) (ProjectEnvironmentCloneConfigurationFence, error) {
		var zero ProjectEnvironmentCloneConfigurationFence
		if op.Status != CloneOperationCompensating {
			return zero, ErrConflict
		}
		if row.State == "open" || pgUUIDString(row.OperationID) != op.ID {
			return zero, nil // committed abandonment may now have a different owner
		}
		if _, err := cloneConfigurationFenceFromSQL(op, row); err != nil {
			return zero, err
		}
		n, err := sqlc.New().AbandonProjectEnvironmentCloneConfiguration(ctx, tx, sqlc.AbandonProjectEnvironmentCloneConfigurationParams{
			ProjectID: mustPgUUID(op.ProjectID), AccountID: mustPgUUID(op.AccountID), OperationID: mustPgUUID(op.ID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if err != nil {
			return zero, mapErr(err)
		}
		if n != 1 {
			return zero, ErrConflict
		}
		return zero, nil
	})
	return err
}
