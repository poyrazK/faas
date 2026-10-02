package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresSnapshotRestoreFromSQL(r sqlc.ProjectEnvironmentClonePostgresSnapshotRestore) ProjectEnvironmentClonePostgresSnapshotRestore {
	return ProjectEnvironmentClonePostgresSnapshotRestore{OperationID: pgUUIDString(r.OperationID), SourceDatabaseID: pgUUIDString(r.SourceDatabaseID),
		AccountID: pgUUIDString(r.AccountID), TargetOwnerID: pgUUIDString(r.TargetOwnerID), BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint,
		State: r.State, TargetProviderResourceID: r.TargetProviderResourceID.String, TargetCreatedAt: r.TargetCreatedAt.Time,
		RequestStartedAt: r.RequestStartedAt.Time, ObservedAt: r.ObservedAt.Time, RestoredAt: r.RestoredAt.Time,
		DeletionStartedAt: r.DeletionStartedAt.Time, DeletedAt: r.DeletedAt.Time, DeletionOperations: string(r.DeleteOperationIds),
		AdoptedDatabaseID: pgUUIDString(r.AdoptedDatabaseID), AdoptedAt: r.AdoptedAt.Time}
}

func cloneSnapshotRestoreContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string, reservation bool) (ProjectEnvironmentCloneOperation, ProjectEnvironmentClonePostgresSnapshot, error) {
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	q := new(sqlc.Queries)
	if reservation {
		if op.Status != CloneOperationCapturing {
			return op, ProjectEnvironmentClonePostgresSnapshot{}, ErrConflict
		}
		if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
			return op, ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
		}
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	source, resource, point, err := capturedCloneDatabaseReservation(op, views, sourceID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	if op.Status == CloneOperationCapturing && (resource.TargetID != "" || resource.Status != "captured") {
		return op, ProjectEnvironmentClonePostgresSnapshot{}, ErrConflict
	}
	if reservation {
		live, err := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(sourceID)})
		if err != nil {
			return op, ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
		}
		if live.State != "ready" || live.DeletedAt.Valid || live.BackendID != source.BackendID || live.BackendFingerprint != source.BackendFingerprint ||
			live.ProviderResourceID.String != source.ProviderResourceID || live.DataResourceID.String != source.DataResourceID {
			return op, ProjectEnvironmentClonePostgresSnapshot{}, ErrConflict
		}
	}
	snapshot, err := readClonePostgresSnapshotTx(ctx, tx, op, sourceID)
	if err != nil {
		return op, snapshot, err
	}
	if err := validateClonePostgresSnapshotSource(snapshot, op, source, resource, point); err != nil {
		return op, snapshot, err
	}
	if op.Status != CloneOperationCompensating && (snapshot.State != "retained" || snapshot.ProviderSnapshotID == "") {
		return op, snapshot, ErrConflict
	}
	return op, snapshot, nil
}

func readCloneSnapshotRestoreTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, snapshot ProjectEnvironmentClonePostgresSnapshot) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresSnapshotRestoreParams{
		OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(snapshot.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	actual := clonePostgresSnapshotRestoreFromSQL(r)
	if actual.OperationID != op.ID || actual.AccountID != op.AccountID || actual.SourceDatabaseID != snapshot.SourceDatabaseID ||
		actual.BackendID != snapshot.BackendID || actual.BackendFingerprint != snapshot.BackendFingerprint ||
		!validCloneCredentialSourceID(actual.TargetOwnerID) || actual.TargetOwnerID == snapshot.SourceDatabaseID {
		return actual, ErrConflict
	}
	if actual.AdoptedDatabaseID != "" {
		if err := validateCloneNativeAdoptedDatabaseTx(ctx, tx, op, actual); err != nil {
			return actual, err
		}
	}
	return actual, nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, limit int) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) || limit < 1 {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, true)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, err
	}
	receipt, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if errors.Is(err, ErrNotFound) {
		q := new(sqlc.Queries)
		count, countErr := q.CountProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID))
		if countErr != nil {
			return receipt, mapErr(countErr)
		}
		if count >= int64(limit) {
			return receipt, ErrQuotaExceeded
		}
		r, insertErr := q.InsertProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresSnapshotRestoreParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if errors.Is(insertErr, pgx.ErrNoRows) {
			return receipt, ErrConflict
		}
		if insertErr != nil {
			return receipt, mapErr(insertErr)
		}
		receipt, err = clonePostgresSnapshotRestoreFromSQL(r), nil
	}
	if err != nil {
		return receipt, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresSnapshotRestoreForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, err
	}
	receipt, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return receipt, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshotRestore, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	if err != nil || op.Status != CloneOperationCapturing {
		if err == nil {
			err = ErrConflict
		}
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, false, err
	}
	receipt, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return receipt, false, err
	}
	dispatch := receipt.State == "reserved"
	if dispatch {
		r, err := new(sqlc.Queries).ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequest(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresSnapshotRestoreRequestParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if errors.Is(err, pgx.ErrNoRows) {
			return receipt, false, ErrConflict
		}
		if err != nil {
			return receipt, false, mapErr(err)
		}
		receipt = clonePostgresSnapshotRestoreFromSQL(r)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, false, mapErr(err)
	}
	return receipt, dispatch, nil
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observed ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	if err != nil || op.Status != CloneOperationCapturing {
		if err == nil {
			err = ErrConflict
		}
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, err
	}
	receipt, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return receipt, err
	}
	if err := validateCloneSnapshotRestoreObservation(snapshot, receipt, observed); err != nil {
		return receipt, err
	}
	r, err := new(sqlc.Queries).RecordProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresSnapshotRestoreParams{
		OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
		TargetProviderResourceID: observed.TargetProviderResourceID, TargetCreatedAt: pgtype.Timestamptz{Time: observed.TargetCreatedAt, Valid: true}, Restored: observed.Restored})
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, ErrConflict
	}
	if err != nil {
		return receipt, mapErr(err)
	}
	return clonePostgresSnapshotRestoreFromSQL(r), mapErr(tx.Commit(ctx))
}
