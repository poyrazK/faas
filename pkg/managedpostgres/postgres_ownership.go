package managedpostgres

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PostgresStore) DatabaseOwnership(ctx context.Context, id string) (Database, error) {
	key, err := postgresUUID(id)
	if err != nil {
		return Database{}, ErrNotFound
	}
	row, err := sqlc.New().ReadManagedPostgresOwnership(ctx, s.catalog, key)
	return databaseFromSQL(row), mapPostgresError(err)
}
func (s *PostgresStore) BindingOwnership(ctx context.Context, id string) (Binding, error) {
	key, err := postgresUUID(id)
	if err != nil {
		return Binding{}, ErrNotFound
	}
	row, err := sqlc.New().ReadManagedPostgresBindingOwnership(ctx, s.catalog, key)
	return bindingFromSQL(row), mapPostgresError(err)
}

func (s *PostgresStore) lockReservationOwner(ctx context.Context, tx sqlc.DBTX, database Database) error {
	q := sqlc.New()
	owner, err := q.LockManagedPostgresReservationOwner(ctx, tx, sqlc.LockManagedPostgresReservationOwnerParams{OrgID: databaseNullableUUID(database.OrgID), AccountID: databaseNullableUUID(database.AccountID)})
	if err != nil {
		return err
	}
	if !owner.PersonalOrg && owner.ResourceOwnershipEnabled {
		return nil
	}
	_, err = q.LockManagedPostgresLifecycleAccount(ctx, tx, databaseNullableUUID(database.AccountID))
	return err
}

// reservationTakesRestoreSlot decides a reservation at or past the owner's
// database limit (ADR-20261031123259): only a restore may go one database
// past it, and only while no other database of the owner holds the slot;
// a restore that must fit inside the limit never does. It runs under the
// owner's reservation lock.
func (s *PostgresStore) reservationTakesRestoreSlot(ctx context.Context, tx sqlc.DBTX, database Database, active int64, limit int) (bool, error) {
	if active < int64(limit) {
		return false, nil
	}
	if database.RestoreSourceDatabaseID == "" || !restoreSlotAllowed(ctx) {
		return false, ErrQuotaExceeded
	}
	q := sqlc.New()
	owner, err := q.LockManagedPostgresReservationOwner(ctx, tx, sqlc.LockManagedPostgresReservationOwnerParams{OrgID: databaseNullableUUID(database.OrgID), AccountID: databaseNullableUUID(database.AccountID)})
	if err != nil {
		return false, mapPostgresError(err)
	}
	params := sqlc.ManagedPostgresRestoreSlotHeldParams{AccountID: databaseNullableUUID(database.AccountID)}
	if !owner.PersonalOrg && owner.ResourceOwnershipEnabled {
		params.OrgID = owner.ID
	}
	held, err := q.ManagedPostgresRestoreSlotHeld(ctx, tx, params)
	if err != nil {
		return false, mapPostgresError(err)
	}
	if held {
		return false, ErrRestoreSlotInUse
	}
	if active >= int64(limit)+api.ManagedPostgresRestoreSlots {
		return false, ErrQuotaExceeded
	}
	return true, nil
}

func (s *PostgresStore) countReservationOwner(ctx context.Context, tx sqlc.DBTX, database Database) (int64, error) {
	q := sqlc.New()
	owner, err := q.LockManagedPostgresReservationOwner(ctx, tx, sqlc.LockManagedPostgresReservationOwnerParams{OrgID: databaseNullableUUID(database.OrgID), AccountID: databaseNullableUUID(database.AccountID)})
	if err != nil {
		return 0, err
	}
	if !owner.PersonalOrg && owner.ResourceOwnershipEnabled {
		return q.CountManagedPostgresOrgDatabases(ctx, tx, owner.ID)
	}
	return q.CountManagedPostgresLifecycleDatabases(ctx, tx, databaseNullableUUID(database.AccountID))
}
