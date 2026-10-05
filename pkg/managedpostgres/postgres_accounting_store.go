package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PostgresStore) BeginAccounting(ctx context.Context, databaseID, leaseToken string, now time.Time) error {
	if leaseToken == "" || now.IsZero() {
		return ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	count, err := sqlc.New().BeginManagedPostgresAccounting(ctx, s.pool, sqlc.BeginManagedPostgresAccountingParams{
		ID: id, LeaseToken: leaseToken, Now: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PostgresStore) RecordDiscoveredResource(ctx context.Context, expected Database, providerResourceID string, now time.Time) error {
	if providerResourceID == "" || now.IsZero() {
		return ErrInvalid
	}
	id, err := postgresUUID(expected.ID)
	if err != nil {
		return err
	}
	account, err := postgresUUID(expected.AccountID)
	if err != nil {
		return err
	}
	count, err := sqlc.New().RecordManagedPostgresDiscoveredResource(ctx, s.pool, sqlc.RecordManagedPostgresDiscoveredResourceParams{
		ID: id, AccountID: account, BackendID: expected.BackendID, BackendFingerprint: expected.BackendFingerprint,
		ProviderResourceID: providerResourceID, Now: pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return mapPostgresError(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}
