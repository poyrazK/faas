package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ DeletionScheduleStore = (*PostgresStore)(nil)

// ClaimScheduledDelete locks the database and checks its dependants in the
// same transaction, as ClaimDelete does, so a concurrent binding or restore
// reservation either commits first and blocks the delete or sees deleting.
func (s *PostgresStore) ClaimScheduledDelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil, purgeAfter time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) || !purgeAfter.After(leaseUntil) {
		return Database{}, ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	tx, err := s.catalog.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Database{}, fmt.Errorf("managed postgres: begin scheduled delete claim: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	row, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: account, ID: id})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	current := databaseFromSQL(row)
	if current.State != StateReady || (!current.LeaseUntil.IsZero() && current.LeaseUntil.After(now)) {
		return Database{}, ErrConflict
	}
	dependants, err := q.ReadManagedPostgresLifecycleDependants(ctx, tx, id)
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if dependants.HasBindings || dependants.HasRestoreDescendants || dependants.HasCloneSnapshotHolds || dependants.HasCloneWriteFenceHolds {
		return Database{}, ErrConflict
	}
	row, err = q.ClaimManagedPostgresScheduledDelete(ctx, tx, sqlc.ClaimManagedPostgresScheduledDeleteParams{
		AccountID: account, DatabaseID: id, LeaseToken: leaseToken, LeaseUntil: databaseNullableTime(leaseUntil),
		PurgeAfter: databaseNullableTime(purgeAfter), At: databaseNullableTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return databaseFromSQL(row), nil
}

func (s *PostgresStore) ClaimUndelete(ctx context.Context, accountID, databaseID, leaseToken string, now, leaseUntil time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() || !leaseUntil.After(now) {
		return Database{}, ErrInvalid
	}
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).ClaimManagedPostgresUndelete(ctx, s.catalog, sqlc.ClaimManagedPostgresUndeleteParams{
		AccountID: account, DatabaseID: id, LeaseToken: leaseToken, LeaseUntil: databaseNullableTime(leaseUntil), At: databaseNullableTime(now)})
	if !errors.Is(err, pgx.ErrNoRows) {
		return databaseFromSQL(row), mapPostgresError(err)
	}
	current, err := s.Get(ctx, accountID, databaseID)
	if err != nil {
		return Database{}, err
	}
	return Database{}, undeleteRefusal(current, now)
}

func (s *PostgresStore) CancelScheduledDelete(ctx context.Context, databaseID, leaseToken string, now time.Time) (Database, error) {
	if leaseToken == "" || now.IsZero() {
		return Database{}, ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).CancelManagedPostgresScheduledDelete(ctx, s.catalog, sqlc.CancelManagedPostgresScheduledDeleteParams{
		DatabaseID: id, LeaseToken: leaseToken, At: databaseNullableTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	return databaseFromSQL(row), mapPostgresError(err)
}

// undeleteRefusal says why a database cannot be undeleted at now: a window
// that is open but leased is contention; anything else is gone.
func undeleteRefusal(database Database, now time.Time) error {
	if database.Restorable(now) {
		return ErrConflict
	}
	if database.State == StateDeleting || database.State == StateDeleted {
		return ErrNotFound
	}
	return ErrConflict
}
