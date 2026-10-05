package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ MigrationCommitRecoveryStore = (*PgStore)(nil)

func (s *PgStore) ResolveInstanceMigrationCommit(ctx context.Context, attempt MigrationCommitAttempt) (MigrationCommitResolution, error) {
	if err := validateMigrationCommitAttempt(attempt); err != nil {
		return MigrationCommitRetained, err
	}
	for _, id := range []string{attempt.InstanceID, attempt.SourceNodeID, attempt.DestinationNodeID} {
		if _, err := uuid.Parse(id); err != nil {
			return MigrationCommitRetained, ErrInvalidArgument
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.LockInstanceMigrationCommit(ctx, tx, mustPgUUID(attempt.InstanceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return MigrationCommitObsolete, nil
	}
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	instance := Instance{ID: pgUUIDString(row.ID), NodeID: pgUUIDString(row.NodeID), State: row.State,
		WakeID: pgUUIDString(row.WakeID), LeaseToken: row.LeaseToken.String}
	if row.MigratedFromNodeID.Valid {
		from := pgUUIDString(row.MigratedFromNodeID)
		instance.MigratedFromNodeID = &from
	}
	resolution := classifyMigrationCommit(instance, attempt)
	if resolution != MigrationCommitAborted || instance.LeaseToken != attempt.LeaseToken {
		return resolution, nil // read-only; releasing the lock cannot fail a commit
	}
	count, err := q.AbortLockedInstanceMigration(ctx, tx, sqlc.AbortLockedInstanceMigrationParams{
		InstanceID: row.ID, SourceNodeID: row.NodeID, ExpectedState: row.State, LeaseToken: instance.LeaseToken, SourceWakeID: row.WakeID,
	})
	if err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	if count != 1 {
		return MigrationCommitRetained, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return MigrationCommitRetained, mapErr(err)
	}
	return MigrationCommitAborted, nil
}
