package managedpostgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CreationReceiptStore = (*PostgresStore)(nil)

func (s *PostgresStore) RecordCreationReceipt(ctx context.Context, r CreationReceipt, lease string) error {
	if r.Validate() != nil {
		return ErrInvalid
	}
	account, err := postgresUUID(r.AccountID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	if r.Kind == "restore" {
		id, err := postgresUUID(r.DatabaseID)
		if err != nil {
			return err
		}
		row, err := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: account, ID: id})
		if err != nil {
			return mapPostgresError(err)
		}
		// The clock is read after the row lock; a queued stale worker cannot append
		// custody under an expired lease or a newer target generation.
		clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
		if err != nil {
			return mapPostgresError(err)
		}
		d := databaseFromSQL(row)
		if lease == "" || d.LeaseToken != lease || !d.LeaseUntil.After(clock.Time) || d.State != StateProvisioning || !d.AccountingRequired ||
			!sameCreationReceipt(r, restoreCreationReceipt(d, r.Acknowledgement)) || d.RestoreSourceResourceID != r.Acknowledgement.SourceResourceID {
			return ErrConflict
		}
	} else {
		// Snapshot custody is appended only to a dispatched durable intent. This
		// write grants no clone lease, retention, correctness or readiness authority.
		owner, err := q.ValidateManagedPostgresSnapshotCreationIntent(ctx, tx, sqlc.ValidateManagedPostgresSnapshotCreationIntentParams{
			ResourceID: r.ResourceID, BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint,
			SourceResourceID: r.Acknowledgement.SourceResourceID, PointInTime: databaseNullableTime(r.PointInTime)})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return mapPostgresError(err)
		}
		if owner != account {
			return ErrConflict
		}
	}
	_, err = q.RecordManagedPostgresCreationReceipt(ctx, tx, sqlc.RecordManagedPostgresCreationReceiptParams{
		Kind: r.Kind, ResourceID: r.ResourceID, AccountID: account, DatabaseID: databaseNullableUUID(r.DatabaseID),
		BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, Generation: r.Generation, PointInTime: databaseNullableTime(r.PointInTime),
		SourceResourceID: r.Acknowledgement.SourceResourceID, ProviderResourceID: r.Acknowledgement.ProviderResourceID,
		ProviderCreatedAt: databaseNullableTime(r.Acknowledgement.CreatedAt)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapPostgresError(err)
	}
	return mapPostgresError(tx.Commit(ctx))
}

func (s *PostgresStore) GetCreationReceipt(ctx context.Context, kind, backend, resource string) (CreationReceipt, error) {
	row, err := new(sqlc.Queries).ReadManagedPostgresCreationReceipt(ctx, s.pool, sqlc.ReadManagedPostgresCreationReceiptParams{Kind: kind, BackendID: backend, ResourceID: resource})
	if err != nil {
		return CreationReceipt{}, mapPostgresError(err)
	}
	return creationReceiptFromSQL(row), nil
}

func creationReceiptFromSQL(row sqlc.ManagedPostgresCreationReceipt) CreationReceipt {
	return CreationReceipt{Kind: row.Kind, ResourceID: row.ResourceID, AccountID: postgresUUIDString(row.AccountID), DatabaseID: postgresUUIDString(row.DatabaseID),
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, Generation: row.Generation, PointInTime: row.PointInTime.Time,
		Acknowledgement: CreationAcknowledgement{ProviderResourceID: row.ProviderResourceID, SourceResourceID: row.SourceResourceID, CreatedAt: row.ProviderCreatedAt.Time}, CleanupStartedAt: row.CleanupStartedAt.Time}
}

func postgresUUIDString(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return uuid.UUID(value.Bytes).String()
}

func (s *PostgresStore) RecordCreationCleanup(ctx context.Context, r CreationReceipt, lease string) error {
	if r.Validate() != nil {
		return ErrInvalid
	}
	account, err := postgresUUID(r.AccountID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapPostgresError(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	if r.Kind == "restore" {
		id, err := postgresUUID(r.DatabaseID)
		if err != nil {
			return err
		}
		row, err := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: account, ID: id})
		if err != nil {
			return mapPostgresError(err)
		}
		clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
		if err != nil {
			return mapPostgresError(err)
		}
		d := databaseFromSQL(row)
		if lease == "" || d.LeaseToken != lease || !d.LeaseUntil.After(clock.Time) || d.State != StateDeleting || !sameCreationReceipt(r, restoreCreationReceipt(d, r.Acknowledgement)) || d.RestoreSourceResourceID != r.Acknowledgement.SourceResourceID {
			return ErrConflict
		}
	} else {
		owner, err := q.ValidateManagedPostgresSnapshotCreationIntent(ctx, tx, sqlc.ValidateManagedPostgresSnapshotCreationIntentParams{ResourceID: r.ResourceID, BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, SourceResourceID: r.Acknowledgement.SourceResourceID, PointInTime: databaseNullableTime(r.PointInTime), Cleanup: true})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return mapPostgresError(err)
		}
		if owner != account {
			return ErrConflict
		}
	}
	row, err := q.ReadManagedPostgresCreationReceipt(ctx, tx, sqlc.ReadManagedPostgresCreationReceiptParams{Kind: r.Kind, BackendID: r.BackendID, ResourceID: r.ResourceID})
	if err != nil {
		return mapPostgresError(err)
	}
	if !sameCreationReceipt(creationReceiptFromSQL(row), r) {
		return ErrConflict
	}
	_, err = q.MarkManagedPostgresCreationCleanup(ctx, tx, sqlc.MarkManagedPostgresCreationCleanupParams{Kind: r.Kind, BackendID: r.BackendID, ResourceID: r.ResourceID, ProviderResourceID: r.Acknowledgement.ProviderResourceID, ProviderCreatedAt: databaseNullableTime(r.Acknowledgement.CreatedAt)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapPostgresError(err)
	}
	return mapPostgresError(tx.Commit(ctx))
}
