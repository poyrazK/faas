package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cloneDatabaseReservationTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneOperation, ProjectEnvironmentClonePostgresBinding, ProjectEnvironmentCloneResource, time.Time, error) {
	identity := lease.Operation
	op, err := lockCloneWorkloadOperationTx(ctx, tx, identity.AccountID, identity.ProjectID, identity.ID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return op, ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, err
	}
	if op.Status != CloneOperationCopying && op.Status != CloneOperationPublishing {
		return op, ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBinding{}, ProjectEnvironmentCloneResource{}, time.Time{}, err
	}
	source, resource, point, err := capturedCloneDatabaseReservation(op, views, sourceID)
	return op, source, resource, point, err
}

func validateCloneDatabaseReservation(op ProjectEnvironmentCloneOperation, source ProjectEnvironmentClonePostgresBinding, resource ProjectEnvironmentCloneResource, point time.Time, actual sqlc.ManagedPostgresDatabase) error {
	if pgUUIDString(actual.ID) == "" || pgUUIDString(actual.ID) == source.DatabaseID || pgUUIDString(actual.AccountID) != op.AccountID ||
		actual.Name != ProjectEnvironmentCloneDatabaseName(op, source.DatabaseID) || resource.TargetID != "" && resource.TargetID != pgUUIDString(actual.ID) ||
		pgUUIDString(actual.EnvironmentCloneOperationID) != op.ID || actual.Region != source.Region || int(actual.PostgresMajor) != source.PostgresMajor ||
		actual.ServiceClass != source.ServiceClass || actual.Availability != source.Availability || actual.ScaleToZero != source.ScaleToZero ||
		actual.StorageLimitBytes != source.StorageLimitBytes || actual.RestoreWindowSeconds != source.RestoreWindowSeconds ||
		actual.BackendID != source.BackendID || actual.BackendFingerprint != source.BackendFingerprint ||
		pgUUIDString(actual.RestoreSourceDatabaseID) != source.DatabaseID || !actual.RestoreSourceResourceID.Valid || actual.RestoreSourceResourceID.String != source.ProviderResourceID ||
		!actual.RestorePointInTime.Valid || !actual.RestorePointInTime.Time.Equal(point) || actual.DeletedAt.Valid ||
		(actual.State != "provisioning" && actual.State != "ready") || actual.DesiredGeneration != 1 ||
		actual.ProviderResourceID.Valid && actual.ProviderResourceID.String == source.ProviderResourceID ||
		actual.State == "ready" && (!actual.ProviderResourceID.Valid || actual.ProviderResourceID.String == "" || actual.ObservedGeneration != 1) {
		return ErrConflict
	}
	return nil
}

func readCloneDatabaseReservationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, sourceID string) (sqlc.ManagedPostgresDatabase, error) {
	q := new(sqlc.Queries)
	actual, err := q.ReadProjectEnvironmentCloneDatabaseReservation(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseReservationParams{
		AccountID: mustPgUUID(op.AccountID), EnvironmentCloneOperationID: mustPgUUID(op.ID), RestoreSourceDatabaseID: mustPgUUID(sourceID)})
	if errors.Is(err, pgx.ErrNoRows) {
		// A foreign or previously deleted customer reservation may hold the
		// deterministic name. It must be rejected, never adopted or replaced.
		return q.ReadProjectEnvironmentCloneDatabaseByName(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseByNameParams{
			AccountID: mustPgUUID(op.AccountID), Name: ProjectEnvironmentCloneDatabaseName(op, sourceID)})
	}
	return actual, err
}

func (s *PgStore) ReserveProjectEnvironmentCloneDatabase(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, limit int) (ProjectEnvironmentCloneDatabaseTarget, bool, error) {
	if !validCloneLeaseIdentity(lease) || sourceID == "" || limit < 1 {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, source, resource, point, err := cloneDatabaseReservationTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	if op.Status != CloneOperationCopying {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
	}
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
	}
	actual, err := readCloneDatabaseReservationTx(ctx, tx, op, sourceID)
	created := false
	if errors.Is(err, pgx.ErrNoRows) {
		if resource.TargetID != "" || resource.Status != "captured" {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
		}
		if _, err := q.LockManagedPostgresCustomerDatabase(ctx, tx, sqlc.LockManagedPostgresCustomerDatabaseParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(sourceID)}); err != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
		}
		live, sourceErr := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(sourceID)})
		if sourceErr != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(sourceErr)
		}
		now, clockErr := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
		if clockErr != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, clockErr
		}
		if live.State != "ready" || live.ProviderResourceID.String != source.ProviderResourceID || live.BackendID != source.BackendID || live.BackendFingerprint != source.BackendFingerprint ||
			!cloneDatabaseRestorePointRetained(point, now.Time, source.RestoreWindowSeconds) || !cloneDatabaseRestorePointRetained(point, now.Time, live.RestoreWindowSeconds) {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrConflict
		}
		count, countErr := q.CountProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID))
		if countErr != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, countErr
		}
		if count >= int64(limit) {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, ErrQuotaExceeded
		}
		// A quota or source lock wait must not extend an expired clone lease.
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return ProjectEnvironmentCloneDatabaseTarget{}, false, err
		}
		actual, err = q.InsertProjectEnvironmentCloneDatabase(ctx, tx, sqlc.InsertProjectEnvironmentCloneDatabaseParams{
			ID: mustPgUUID(uuid.NewString()), AccountID: mustPgUUID(op.AccountID), Name: ProjectEnvironmentCloneDatabaseName(op, sourceID),
			Region: source.Region, PostgresMajor: int16(source.PostgresMajor), ServiceClass: source.ServiceClass, Availability: source.Availability, ScaleToZero: source.ScaleToZero,
			StorageLimitBytes: source.StorageLimitBytes, RestoreWindowSeconds: source.RestoreWindowSeconds, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
			RestoreSourceDatabaseID: mustPgUUID(sourceID), RestoreSourceResourceID: pgtype.Text{String: source.ProviderResourceID, Valid: true},
			RestorePointInTime: pgtype.Timestamptz{Time: point, Valid: true}, EnvironmentCloneOperationID: mustPgUUID(op.ID)})
		created = true
	}
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
	}
	if err := validateCloneDatabaseReservation(op, source, resource, point, actual); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, false, mapErr(err)
	}
	return ProjectEnvironmentCloneDatabaseTarget{ID: pgUUIDString(actual.ID), State: actual.State}, created, nil
}

func cloneDatabaseRestorePointRetained(point, now time.Time, seconds int64) bool {
	if seconds <= 0 || !point.Before(now) {
		return false
	}
	// Captured RFC3339 years fit Unix seconds. Avoid duration multiplication
	// and subtraction saturation for operator-configured long retention.
	wholeSeconds := now.Unix() - point.Unix()
	return wholeSeconds < seconds || wholeSeconds == seconds && now.Nanosecond() <= point.Nanosecond()
}

func (s *PgStore) ProjectEnvironmentCloneDatabaseForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneDatabaseTarget, error) {
	if !validCloneLeaseIdentity(lease) || sourceID == "" {
		return ProjectEnvironmentCloneDatabaseTarget{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, source, resource, point, err := cloneDatabaseReservationTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, err
	}
	actual, err := readCloneDatabaseReservationTx(ctx, tx, op, sourceID)
	if errors.Is(err, pgx.ErrNoRows) && resource.TargetID != "" {
		return ProjectEnvironmentCloneDatabaseTarget{}, ErrConflict
	}
	if err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, mapErr(err)
	}
	if err := validateCloneDatabaseReservation(op, source, resource, point, actual); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return ProjectEnvironmentCloneDatabaseTarget{}, err
	}
	return ProjectEnvironmentCloneDatabaseTarget{ID: pgUUIDString(actual.ID), State: actual.State}, tx.Commit(ctx)
}
