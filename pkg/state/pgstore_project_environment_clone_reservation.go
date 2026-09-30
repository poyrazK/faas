package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// The project lock serializes the environment table with reservations in the
// operation table. The operation lock keeps a worker's revision valid through
// the configuration transaction, including concurrent failure/compensation.
func lockProjectEnvironmentCloneReservationTx(ctx context.Context, tx pgx.Tx, clone ProjectEnvironmentClone) error {
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneProject(ctx, tx, sqlc.LockProjectEnvironmentCloneProjectParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID),
	}); err != nil {
		return mapErr(err)
	}
	row, err := q.LockProjectEnvironmentCloneTargetReservation(ctx, tx, sqlc.LockProjectEnvironmentCloneTargetReservationParams{
		AccountID: mustPgUUID(clone.AccountID), ProjectID: mustPgUUID(clone.ProjectID), TargetEnvironment: clone.TargetSlug,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		if clone.CloneOperationID != "" || clone.CloneOperationRevision != 0 {
			return ErrConflict
		}
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	return validateProjectEnvironmentCloneOwner(clone, row.ID, row.SourceEnvironment, row.Status, row.Revision)
}
