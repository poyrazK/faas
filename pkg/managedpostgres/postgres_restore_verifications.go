package managedpostgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RestoreVerificationStore = (*PostgresStore)(nil)

// RecordRestoreVerification appends the first verification of a restore
// target under its current provisioning lease (ADR-1259). The row lock and
// the server clock read after it fence a stale worker, as for custody receipts.
func (s *PostgresStore) RecordRestoreVerification(ctx context.Context, database Database, v RestoreVerification) error {
	account, err := postgresUUID(database.AccountID)
	if err != nil {
		return err
	}
	id, err := postgresUUID(database.ID)
	if err != nil {
		return err
	}
	tx, err := s.catalog.Begin(ctx)
	if err != nil {
		return mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	row, err := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: account, ID: id})
	if err != nil {
		return mapPostgresError(err)
	}
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return mapPostgresError(err)
	}
	d := databaseFromSQL(row)
	if err := v.Validate(d.ProviderResourceID, RestoreRequest{SourceResourceID: d.RestoreSourceResourceID, PointInTime: d.RestorePointInTime}); err != nil {
		return err
	}
	if database.LeaseToken == "" || d.LeaseToken != database.LeaseToken || !d.LeaseUntil.After(clock.Time) || d.State != StateProvisioning ||
		d.DesiredGeneration != database.DesiredGeneration || v.PointInTime.After(clock.Time) {
		return ErrConflict
	}
	_, err = q.RecordManagedPostgresRestoreVerification(ctx, tx, sqlc.RecordManagedPostgresRestoreVerificationParams{
		DatabaseID: id, AccountID: account, BackendID: d.BackendID, BackendFingerprint: d.BackendFingerprint, Generation: d.DesiredGeneration,
		ProviderResourceID: v.ProviderResourceID, SourceResourceID: v.SourceResourceID, PointInTime: databaseNullableTime(v.PointInTime), Evidence: v.Evidence})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapPostgresError(err)
	}
	return mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) GetRestoreVerification(ctx context.Context, accountID, databaseID string) (RestoreVerification, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return RestoreVerification{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return RestoreVerification{}, err
	}
	row, err := new(sqlc.Queries).ReadManagedPostgresRestoreVerification(ctx, s.catalog, sqlc.ReadManagedPostgresRestoreVerificationParams{AccountID: account, DatabaseID: id})
	if err != nil {
		return RestoreVerification{}, mapPostgresError(err)
	}
	return RestoreVerification{ProviderResourceID: row.ProviderResourceID, SourceResourceID: row.SourceResourceID,
		PointInTime: row.PointInTime.Time, Evidence: row.Evidence}, nil
}
