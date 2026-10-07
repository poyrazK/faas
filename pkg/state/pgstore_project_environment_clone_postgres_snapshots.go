package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresSnapshotFromSQL(row sqlc.ProjectEnvironmentClonePostgresSnapshot) ProjectEnvironmentClonePostgresSnapshot {
	return ProjectEnvironmentClonePostgresSnapshot{OperationID: pgUUIDString(row.OperationID), SourceDatabaseID: pgUUIDString(row.SourceDatabaseID),
		SourceVersion: row.SourceVersion, BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint,
		SourceProviderResourceID: row.SourceProviderResourceID, SourceDataResourceID: row.SourceDataResourceID,
		ProviderSnapshotID: row.ProviderSnapshotID.String, State: row.State, CapturePoint: row.CapturePoint.Time,
		SnapshotCreatedAt: row.SnapshotCreatedAt.Time, ObservedAt: row.ObservedAt.Time, CleanupObservedAt: row.CleanupObservedAt.Time, RequestStartedAt: row.RequestStartedAt.Time}
}

func clonePostgresSnapshotLeaseTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, error) {
	op, err := lockCloneWorkloadOperationTx(ctx, tx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID)
	if err != nil {
		return op, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return op, err
	}
	switch op.Status {
	case CloneOperationCapturing, CloneOperationCopying, CloneOperationPublishing, CloneOperationCompensating:
		return op, nil
	default:
		return op, ErrConflict
	}
}

func readClonePostgresSnapshotTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, sourceID string) (ProjectEnvironmentClonePostgresSnapshot, error) {
	row, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresSnapshot(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresSnapshotParams{
		OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
	}
	return clonePostgresSnapshotFromSQL(row), nil
}

func validateClonePostgresSnapshotSource(receipt ProjectEnvironmentClonePostgresSnapshot, op ProjectEnvironmentCloneOperation,
	source ProjectEnvironmentClonePostgresBinding, resource ProjectEnvironmentCloneResource, point time.Time) error {
	if receipt.OperationID != op.ID || receipt.SourceDatabaseID != source.DatabaseID || receipt.SourceVersion != resource.SourceVersion ||
		receipt.BackendID != source.BackendID || receipt.BackendFingerprint != source.BackendFingerprint ||
		receipt.SourceProviderResourceID != source.ProviderResourceID || receipt.SourceDataResourceID != source.DataResourceID || !receipt.CapturePoint.Equal(point) {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresSnapshot(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshot, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshot{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentClonePostgresSnapshot{}, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	source, resource, point, err := capturedCloneDatabaseReservation(op, views, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	receipt, err := readClonePostgresSnapshotTx(ctx, tx, op, sourceID)
	if errors.Is(err, ErrNotFound) {
		if resource.TargetID != "" || resource.Status != "captured" {
			return receipt, ErrConflict
		}
		q := new(sqlc.Queries)
		// This is the same row lock as lifecycle deletion. A committed intent
		// becomes a deletion hold even if the provider call never returns.
		live, sourceErr := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{
			AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(sourceID)})
		if sourceErr != nil {
			return receipt, mapErr(sourceErr)
		}
		now, clockErr := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
		if clockErr != nil {
			return receipt, clockErr
		}
		if live.State != "ready" || live.DeletedAt.Valid || live.BackendID != source.BackendID || live.BackendFingerprint != source.BackendFingerprint ||
			live.ProviderResourceID.String != source.ProviderResourceID || live.DataResourceID.String != source.DataResourceID ||
			!cloneDatabaseRestorePointRetained(point, now.Time, source.RestoreWindowSeconds) || !cloneDatabaseRestorePointRetained(point, now.Time, live.RestoreWindowSeconds) {
			return receipt, ErrConflict
		}
		row, insertErr := q.InsertProjectEnvironmentClonePostgresSnapshot(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresSnapshotParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), SourceVersion: resource.SourceVersion,
			BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint, SourceProviderResourceID: source.ProviderResourceID,
			SourceDataResourceID: source.DataResourceID, CapturePoint: pgtype.Timestamptz{Time: point, Valid: true},
			ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if errors.Is(insertErr, pgx.ErrNoRows) {
			return receipt, ErrConflict
		}
		if insertErr != nil {
			return receipt, mapErr(insertErr)
		}
		receipt, err = clonePostgresSnapshotFromSQL(row), nil
	}
	if err != nil {
		return receipt, err
	}
	if err := validateClonePostgresSnapshotSource(receipt, op, source, resource, point); err != nil {
		return receipt, err
	}
	if receipt.State != "capturing" && receipt.State != "requested" && receipt.State != "retained" {
		return receipt, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresSnapshotForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshot, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshot{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	receipt, err := readClonePostgresSnapshotTx(ctx, tx, op, sourceID)
	if err != nil {
		return receipt, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresSnapshot(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observed ProjectEnvironmentClonePostgresSnapshotObservation) (ProjectEnvironmentClonePostgresSnapshot, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshot{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	receipt, err := readClonePostgresSnapshotTx(ctx, tx, op, sourceID)
	if err != nil {
		return receipt, err
	}
	if (op.Status != CloneOperationCapturing || receipt.State != "requested" && receipt.State != "retained") &&
		(op.Status != CloneOperationCompensating || receipt.State != "deleting") {
		return receipt, ErrConflict
	}
	if err := validateCloneSnapshotObservation(receipt, observed); err != nil {
		return receipt, err
	}
	var row sqlc.ProjectEnvironmentClonePostgresSnapshot
	if op.Status == CloneOperationCapturing {
		row, err = new(sqlc.Queries).RetainProjectEnvironmentClonePostgresSnapshot(ctx, tx, sqlc.RetainProjectEnvironmentClonePostgresSnapshotParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderSnapshotID: observed.ProviderSnapshotID,
			SnapshotCreatedAt: pgtype.Timestamptz{Time: observed.CreatedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
	} else {
		row, err = new(sqlc.Queries).RecordProjectEnvironmentClonePostgresSnapshotCleanupIdentity(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresSnapshotCleanupIdentityParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderSnapshotID: observed.ProviderSnapshotID,
			SnapshotCreatedAt: pgtype.Timestamptz{Time: observed.CreatedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, ErrConflict
	}
	if err != nil {
		return receipt, mapErr(err)
	}
	return clonePostgresSnapshotFromSQL(row), mapErr(tx.Commit(ctx))
}

func (s *PgStore) BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshot, error) {
	return s.mutateClonePostgresSnapshotCleanup(ctx, lease, sourceID, false)
}

func (s *PgStore) FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshot, error) {
	return s.mutateClonePostgresSnapshotCleanup(ctx, lease, sourceID, true)
}

func (s *PgStore) mutateClonePostgresSnapshotCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, finish bool) (ProjectEnvironmentClonePostgresSnapshot, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshot{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, err
	}
	if op.Status != CloneOperationCompensating {
		return ProjectEnvironmentClonePostgresSnapshot{}, ErrConflict
	}
	receipt, err := readClonePostgresSnapshotTx(ctx, tx, op, sourceID)
	if err != nil {
		return receipt, err
	}
	if receipt.State == "deleted" {
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return receipt, err
		}
		return receipt, mapErr(tx.Commit(ctx))
	}
	q := new(sqlc.Queries)
	var row sqlc.ProjectEnvironmentClonePostgresSnapshot
	if finish {
		row, err = q.FinishProjectEnvironmentClonePostgresSnapshotCleanup(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresSnapshotCleanupParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
	} else {
		row, err = q.BeginProjectEnvironmentClonePostgresSnapshotCleanup(ctx, tx, sqlc.BeginProjectEnvironmentClonePostgresSnapshotCleanupParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, ErrConflict
	}
	if err != nil {
		return receipt, mapErr(err)
	}
	return clonePostgresSnapshotFromSQL(row), mapErr(tx.Commit(ctx))
}

// Only the worker receiving the first committed dispatch may issue creation.
// An unknown commit or provider outcome recovers by observation, never another
// POST. A not-yet-visible copy retains its source hold and remains retryable.
func (s *PgStore) ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshot, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshot{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, false, err
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentClonePostgresSnapshot{}, false, ErrConflict
	}
	receipt, err := readClonePostgresSnapshotTx(ctx, tx, op, sourceID)
	if err != nil {
		return receipt, false, err
	}
	dispatch := receipt.State == "capturing"
	if dispatch {
		row, err := new(sqlc.Queries).ClaimProjectEnvironmentClonePostgresSnapshotRequest(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresSnapshotRequestParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if errors.Is(err, pgx.ErrNoRows) {
			return receipt, false, ErrConflict
		}
		if err != nil {
			return receipt, false, mapErr(err)
		}
		receipt = clonePostgresSnapshotFromSQL(row)
	} else if receipt.State != "requested" && receipt.State != "retained" {
		return receipt, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentClonePostgresSnapshot{}, false, mapErr(err)
	}
	return receipt, dispatch, nil
}
