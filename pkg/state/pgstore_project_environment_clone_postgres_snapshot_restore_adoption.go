package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validateCloneNativeAdoptedDatabaseTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, receipt ProjectEnvironmentClonePostgresSnapshotRestore) error {
	if receipt.AdoptedDatabaseID == "" || receipt.AdoptedDatabaseID != receipt.TargetOwnerID || receipt.AdoptedAt.IsZero() || receipt.RestoredAt.IsZero() {
		return ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return err
	}
	source, _, point, err := capturedCloneDatabaseReservation(op, views, receipt.SourceDatabaseID)
	if err != nil {
		return err
	}
	actual, err := new(sqlc.Queries).LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(receipt.AdoptedDatabaseID)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrConflict
		}
		return mapErr(err)
	}
	wantState := "provisioning"
	if receipt.State == "deleted" {
		wantState = "deleted"
	}
	if pgUUIDString(actual.ID) != receipt.AdoptedDatabaseID || pgUUIDString(actual.AccountID) != op.AccountID ||
		actual.Name != ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(op, source.DatabaseID) || actual.CloneResourceRole != "checkpoint" ||
		pgUUIDString(actual.EnvironmentCloneOperationID) != op.ID || actual.Region != source.Region || int(actual.PostgresMajor) != source.PostgresMajor ||
		actual.ServiceClass != source.ServiceClass || actual.Availability != source.Availability || actual.ScaleToZero != source.ScaleToZero ||
		actual.StorageLimitBytes != source.StorageLimitBytes || actual.RestoreWindowSeconds != source.RestoreWindowSeconds ||
		actual.BackendID != receipt.BackendID || actual.BackendFingerprint != receipt.BackendFingerprint ||
		pgUUIDString(actual.RestoreSourceDatabaseID) != source.DatabaseID || actual.RestoreSourceResourceID.String != source.DataResourceID ||
		!actual.RestorePointInTime.Valid || !actual.RestorePointInTime.Time.Equal(point) || actual.ProviderResourceID.String != receipt.TargetProviderResourceID ||
		actual.State != wantState || actual.DeletedAt.Valid != (wantState == "deleted") || actual.DataResourceID.Valid ||
		actual.DesiredGeneration != 1 || actual.ObservedGeneration != 0 || actual.LeaseToken.Valid || actual.LeaseUntil.Valid {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneDatabaseTarget, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	// Match quota reservation's account-before-snapshot/fork lock order. The
	// clone operation lock precedes account authority in both paths.
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
	}
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
	}
	receipt, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	created := false
	if receipt.State != "adopted" {
		if receipt.State != "restored" || receipt.RestoredAt.IsZero() || receipt.TargetProviderResourceID == "" {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
		}
		if _, err := q.ReadProjectEnvironmentCloneDatabaseByName(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseByNameParams{
			AccountID: mustPgUUID(op.AccountID), Name: ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(op, sourceID)}); !errors.Is(err, pgx.ErrNoRows) {
			if err != nil {
				return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
			}
			return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
		}
		records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
		if err != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, err
		}
		views, err := cloneBindingViews(records)
		if err != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, err
		}
		source, _, point, err := capturedCloneDatabaseReservation(op, views, sourceID)
		if err != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, err
		}
		_, err = q.InsertProjectEnvironmentCloneDatabase(ctx, tx, sqlc.InsertProjectEnvironmentCloneDatabaseParams{
			ID: mustPgUUID(receipt.TargetOwnerID), AccountID: mustPgUUID(op.AccountID), Name: ProjectEnvironmentClonePostgresSnapshotRestoreDatabaseName(op, sourceID), CloneResourceRole: "checkpoint",
			Region: source.Region, PostgresMajor: int16(source.PostgresMajor), ServiceClass: source.ServiceClass, Availability: source.Availability, ScaleToZero: source.ScaleToZero,
			StorageLimitBytes: source.StorageLimitBytes, RestoreWindowSeconds: source.RestoreWindowSeconds, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
			RestoreSourceDatabaseID: mustPgUUID(sourceID), RestoreSourceResourceID: pgtype.Text{String: source.DataResourceID, Valid: true},
			RestorePointInTime: pgtype.Timestamptz{Time: point, Valid: true}, EnvironmentCloneOperationID: mustPgUUID(op.ID)})
		if err != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
		}
		if _, err := q.PinProjectEnvironmentCloneNativeForkDatabase(ctx, tx, sqlc.PinProjectEnvironmentCloneNativeForkDatabaseParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
			}
			return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
		}
		row, err := q.AdoptProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.AdoptProjectEnvironmentClonePostgresSnapshotRestoreParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
			}
			return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
		}
		receipt, created = clonePostgresSnapshotRestoreFromSQL(row), true
	}
	if err := validateCloneNativeAdoptedDatabaseTx(ctx, tx, op, receipt); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	return ProjectEnvironmentCloneDatabaseTarget{ID: receipt.AdoptedDatabaseID, State: "provisioning"}, created, mapErr(tx.Commit(ctx))
}
