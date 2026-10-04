package managedpostgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func customerDatabase(ctx context.Context, store Store, accountID, databaseID string) (Database, error) {
	if customers, ok := store.(CustomerDatabaseStore); ok {
		return customers.GetCustomerDatabase(ctx, accountID, databaseID)
	}
	database, err := store.Get(ctx, accountID, databaseID)
	if err == nil && database.EnvironmentCloneOperationID != "" {
		return Database{}, ErrNotFound
	}
	return database, err
}

func (s *MemoryStore) GetCustomerDatabase(ctx context.Context, accountID, databaseID string) (Database, error) {
	database, err := s.Get(ctx, accountID, databaseID)
	if err == nil && database.EnvironmentCloneOperationID != "" {
		// MemoryStore has no durable clone catalogue. Unknown owners stay private.
		return Database{}, ErrNotFound
	}
	return database, err
}

func (s *MemoryStore) ListCustomerDatabases(ctx context.Context, accountID string) ([]Database, error) {
	items, err := s.List(ctx, accountID)
	visible := make([]Database, 0, len(items))
	for _, database := range items {
		if database.EnvironmentCloneOperationID == "" {
			visible = append(visible, database)
		}
	}
	return visible, err
}

func (s *PostgresStore) GetCustomerDatabase(ctx context.Context, accountID, databaseID string) (Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return Database{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).GetManagedPostgresCustomerDatabase(ctx, s.pool, sqlc.GetManagedPostgresCustomerDatabaseParams{AccountID: account, ID: id})
	return databaseFromSQL(row), mapPostgresError(err)
}

func (s *PostgresStore) ListCustomerDatabases(ctx context.Context, accountID string) ([]Database, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return nil, err
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresCustomerDatabases(ctx, s.pool, account)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	items := make([]Database, 0, len(rows))
	for _, row := range rows {
		items = append(items, databaseFromSQL(row))
	}
	return items, nil
}

func databaseUUID(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func databaseFromSQL(row sqlc.ManagedPostgresDatabase) Database {
	database := Database{ID: databaseUUID(row.ID), AccountID: databaseUUID(row.AccountID), Name: row.Name,
		Spec: Spec{Region: row.Region, PostgresMajor: int(row.PostgresMajor), Class: ServiceClass(row.ServiceClass), Availability: Availability(row.Availability),
			ScaleToZero: row.ScaleToZero, StorageLimitBytes: row.StorageLimitBytes, RestoreWindowSeconds: row.RestoreWindowSeconds},
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, ProviderResourceID: row.ProviderResourceID.String, DataResourceID: row.DataResourceID.String,
		AccountingRequired:      row.AccountingRequired,
		RestoreSourceDatabaseID: databaseUUID(row.RestoreSourceDatabaseID), RestoreSourceResourceID: row.RestoreSourceResourceID.String,
		RestorePointInTime: row.RestorePointInTime.Time, EnvironmentCloneOperationID: databaseUUID(row.EnvironmentCloneOperationID),
		State: State(row.State), DesiredGeneration: row.DesiredGeneration, ObservedGeneration: row.ObservedGeneration, LastErrorCode: row.LastErrorCode.String,
		LeaseToken: row.LeaseToken.String, LeaseUntil: row.LeaseUntil.Time, AttemptCount: row.AttemptCount, RetryAt: row.RetryAt.Time,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	if row.DeletedAt.Valid {
		at := row.DeletedAt.Time
		database.DeletedAt = &at
	}
	return database
}

var _ CustomerDatabaseStore = (*MemoryStore)(nil)
var _ CustomerDatabaseStore = (*PostgresStore)(nil)
