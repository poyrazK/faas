package managedpostgres

import (
	"context"

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
