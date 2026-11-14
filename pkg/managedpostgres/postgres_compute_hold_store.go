package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PostgresStore) ClaimComputeHold(ctx context.Context, expected Database, token string, now, until time.Time) (Database, error) {
	if token == "" || now.IsZero() || !until.After(now) || !capHoldable(expected) {
		return Database{}, ErrInvalid
	}
	id, err := postgresUUID(expected.ID)
	if err != nil {
		return Database{}, err
	}
	account, err := postgresUUID(expected.AccountID)
	if err != nil {
		return Database{}, err
	}
	row, err := new(sqlc.Queries).ClaimManagedPostgresComputeHold(ctx, s.catalog, sqlc.ClaimManagedPostgresComputeHoldParams{
		DatabaseID: id, AccountID: account, LeaseToken: token, LeaseMilliseconds: until.Sub(now).Milliseconds(),
		State: string(expected.State), Generation: expected.DesiredGeneration, BackendID: expected.BackendID,
		BackendFingerprint: expected.BackendFingerprint, ProviderResourceID: expected.ProviderResourceID,
		DataResourceID: databaseDataResource(expected),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	return databaseFromSQL(row), mapPostgresError(err)
}

func (s *PostgresStore) ReleaseComputeHold(ctx context.Context, databaseID, token string, now time.Time) error {
	if token == "" || now.IsZero() {
		return ErrInvalid
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return err
	}
	n, err := new(sqlc.Queries).ReleaseManagedPostgresComputeHold(ctx, s.catalog, sqlc.ReleaseManagedPostgresComputeHoldParams{DatabaseID: id, LeaseToken: token})
	if err != nil {
		return mapPostgresError(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
