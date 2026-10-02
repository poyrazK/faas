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

func clonePostgresCopyTargetFromSQL(r sqlc.ProjectEnvironmentClonePostgresCopyTarget) ProjectEnvironmentClonePostgresCopyTarget {
	return ProjectEnvironmentClonePostgresCopyTarget{OperationID: pgUUIDString(r.OperationID), SourceDatabaseID: pgUUIDString(r.SourceDatabaseID),
		AccountID: pgUUIDString(r.AccountID), CaptureDatabaseID: pgUUIDString(r.CaptureDatabaseID), TargetDatabaseID: pgUUIDString(r.TargetDatabaseID),
		State: r.State, ProviderResourceID: r.ProviderResourceID.String, RequestStartedAt: r.RequestStartedAt.Time,
		ProviderCreatedAt: r.ProviderCreatedAt.Time, ObservedAt: r.ObservedAt.Time, PreparedAt: r.PreparedAt.Time, RetiredAt: r.RetiredAt.Time}
}

func clonePostgresCopyContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneOperation, ProjectEnvironmentClonePostgresSnapshotRestore, ProjectEnvironmentClonePostgresBinding, ProjectEnvironmentCloneResource, time.Time, error) {
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	var source ProjectEnvironmentClonePostgresBinding
	var resource ProjectEnvironmentCloneResource
	var point time.Time
	if err != nil {
		return op, ProjectEnvironmentClonePostgresSnapshotRestore{}, source, resource, point, err
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCompensating {
		return op, ProjectEnvironmentClonePostgresSnapshotRestore{}, source, resource, point, ErrConflict
	}
	capture, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return op, capture, source, resource, point, err
	}
	// A retired undispatched target can replay after capture cleanup. Every
	// active target below still requires the adopted, retained native input.
	if capture.State != "adopted" && op.Status != CloneOperationCompensating {
		return op, capture, source, resource, point, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return op, capture, source, resource, point, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return op, capture, source, resource, point, err
	}
	source, resource, point, err = capturedCloneDatabaseReservation(op, views, sourceID)
	return op, capture, source, resource, point, err
}

func readClonePostgresCopyTargetTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, capture ProjectEnvironmentClonePostgresSnapshotRestore, source ProjectEnvironmentClonePostgresBinding, resource ProjectEnvironmentCloneResource, point time.Time) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	q := new(sqlc.Queries)
	r, err := q.ReadProjectEnvironmentClonePostgresCopyTarget(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresCopyTargetParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(source.DatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, mapErr(err)
	}
	receipt := clonePostgresCopyTargetFromSQL(r)
	if receipt.OperationID != op.ID || receipt.SourceDatabaseID != source.DatabaseID || receipt.AccountID != op.AccountID ||
		receipt.CaptureDatabaseID != capture.AdoptedDatabaseID || receipt.CaptureDatabaseID == "" ||
		receipt.State != "retired" && capture.State != "adopted" {
		return receipt, ErrConflict
	}
	actual, err := q.LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(receipt.TargetDatabaseID)})
	if err != nil {
		return receipt, mapErr(err)
	}
	if receipt.State == "retired" {
		// Reuse the complete frozen-target validator before checking the
		// terminal catalogue shape. Only undispatched ownership retires here.
		if actual.State != "deleted" || !actual.DeletedAt.Valid || receipt.ProviderResourceID != "" || !receipt.RequestStartedAt.IsZero() {
			return receipt, ErrConflict
		}
		actual.State, actual.DeletedAt = "provisioning", pgtype.Timestamptz{}
	}
	if validateCloneDatabaseReservation(op, source, resource, point, actual) != nil || pgUUIDString(actual.ID) != receipt.TargetDatabaseID ||
		actual.State != "provisioning" || actual.ObservedGeneration != 0 || actual.DataResourceID.Valid || actual.LeaseToken.Valid || actual.LeaseUntil.Valid ||
		actual.ProviderResourceID.Valid != (receipt.ProviderResourceID != "") || actual.ProviderResourceID.String != receipt.ProviderResourceID {
		return receipt, ErrConflict
	}
	return receipt, nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresCopyTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, limit int) (ProjectEnvironmentClonePostgresCopyTarget, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) || limit < 1 {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil || op.Status != CloneOperationCapturing {
		if err == nil {
			err = ErrConflict
		}
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, err
	}
	q := new(sqlc.Queries)
	// Match every managed database reservation's account quota lock order.
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, mapErr(err)
	}
	op, capture, source, resource, point, err := clonePostgresCopyContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, err
	}
	receipt, err := readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
	created := false
	if errors.Is(err, ErrNotFound) {
		if resource.TargetID != "" || resource.Status != "captured" {
			return receipt, false, ErrConflict
		}
		if _, err := readCloneDatabaseReservationTx(ctx, tx, op, sourceID); !errors.Is(err, pgx.ErrNoRows) {
			if err != nil {
				return receipt, false, mapErr(err)
			}
			return receipt, false, ErrConflict
		}
		count, countErr := q.CountProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID))
		if countErr != nil {
			return receipt, false, mapErr(countErr)
		}
		if count >= int64(limit) {
			return receipt, false, ErrQuotaExceeded
		}
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return receipt, false, err
		}
		targetID := uuid.NewString()
		_, err = q.InsertProjectEnvironmentCloneDatabase(ctx, tx, sqlc.InsertProjectEnvironmentCloneDatabaseParams{
			ID: mustPgUUID(targetID), AccountID: mustPgUUID(op.AccountID), Name: ProjectEnvironmentCloneDatabaseName(op, sourceID), CloneResourceRole: "target",
			Region: source.Region, PostgresMajor: int16(source.PostgresMajor), ServiceClass: source.ServiceClass, Availability: source.Availability, ScaleToZero: source.ScaleToZero,
			StorageLimitBytes: source.StorageLimitBytes, RestoreWindowSeconds: source.RestoreWindowSeconds, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
			RestoreSourceDatabaseID: mustPgUUID(sourceID), RestoreSourceResourceID: pgtype.Text{String: source.DataResourceID, Valid: true},
			RestorePointInTime: pgtype.Timestamptz{Time: point, Valid: true}, EnvironmentCloneOperationID: mustPgUUID(op.ID)})
		if err != nil {
			return receipt, false, mapErr(err)
		}
		_, err = q.InsertProjectEnvironmentClonePostgresCopyTarget(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresCopyTargetParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), AccountID: mustPgUUID(op.AccountID),
			CaptureDatabaseID: mustPgUUID(capture.AdoptedDatabaseID), TargetDatabaseID: mustPgUUID(targetID)})
		if err != nil {
			return receipt, false, mapErr(err)
		}
		receipt, err = readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
		created = true
	}
	if err != nil {
		return receipt, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, false, err
	}
	return receipt, created, mapErr(tx.Commit(ctx))
}

// All subsequent operations use the same operation/snapshot/fork/catalogue
// locks and recheck authoritative lease time after lock waits.
func (s *PgStore) mutateClonePostgresCopyTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, action string, observed ProjectEnvironmentClonePostgresCopyTargetObservation) (ProjectEnvironmentClonePostgresCopyTarget, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, capture, source, resource, point, err := clonePostgresCopyContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, false, err
	}
	r, err := readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
	if err != nil {
		return r, false, err
	}
	q, dispatch := new(sqlc.Queries), false
	var row sqlc.ProjectEnvironmentClonePostgresCopyTarget
	switch action {
	case "read":
	case "claim":
		if op.Status != CloneOperationCapturing || r.State == "retired" {
			return r, false, ErrConflict
		}
		if r.State == "reserved" {
			row, err = q.ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresCopyTargetRequestParams{
				OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
			dispatch = true
		}
	case "record":
		if op.Status != CloneOperationCapturing || r.State == "reserved" || r.State == "retired" || !validCloneSnapshotID(observed.ProviderResourceID) ||
			observed.ProviderResourceID == source.ProviderResourceID || observed.ProviderResourceID == source.DataResourceID || observed.ProviderResourceID == capture.TargetProviderResourceID ||
			observed.CreatedAt.Before(capture.TargetCreatedAt) || observed.CreatedAt.Nanosecond()%1000 != 0 ||
			r.ProviderResourceID != "" && (r.ProviderResourceID != observed.ProviderResourceID || !r.ProviderCreatedAt.Equal(observed.CreatedAt)) || r.State == "prepared" && !observed.Prepared {
			return r, false, ErrConflict
		}
		_, err = q.PinProjectEnvironmentClonePostgresCopyTargetDatabase(ctx, tx, sqlc.PinProjectEnvironmentClonePostgresCopyTargetDatabaseParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderResourceID: observed.ProviderResourceID, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if err == nil {
			row, err = q.RecordProjectEnvironmentClonePostgresCopyTarget(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresCopyTargetParams{
				OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderResourceID: observed.ProviderResourceID,
				ProviderCreatedAt: pgtype.Timestamptz{Time: observed.CreatedAt, Valid: true}, Prepared: observed.Prepared, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		}
	case "retire":
		if op.Status != CloneOperationCompensating || r.State != "reserved" && r.State != "retired" {
			return r, false, ErrConflict
		}
		if r.State == "reserved" {
			_, err = q.RetireUndispatchedProjectEnvironmentClonePostgresCopyTargetDatabase(ctx, tx, sqlc.RetireUndispatchedProjectEnvironmentClonePostgresCopyTargetDatabaseParams{
				OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
			if err == nil {
				row, err = q.RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(ctx, tx, sqlc.RetireUndispatchedProjectEnvironmentClonePostgresCopyTargetParams{
					OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
			}
		}
	default:
		return r, false, ErrInvalidArgument
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return r, false, ErrConflict
	}
	if err != nil {
		return r, false, mapErr(err)
	}
	if row.OperationID.Valid {
		r, err = readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
		if err != nil {
			return r, false, err
		}
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, false, err
	}
	return r, dispatch, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresCopyTargetForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	r, _, err := s.mutateClonePostgresCopyTarget(ctx, lease, sourceID, "read", ProjectEnvironmentClonePostgresCopyTargetObservation{})
	return r, err
}

func (s *PgStore) ClaimProjectEnvironmentClonePostgresCopyTargetRequest(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresCopyTarget, bool, error) {
	return s.mutateClonePostgresCopyTarget(ctx, lease, sourceID, "claim", ProjectEnvironmentClonePostgresCopyTargetObservation{})
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresCopyTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observed ProjectEnvironmentClonePostgresCopyTargetObservation) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	r, _, err := s.mutateClonePostgresCopyTarget(ctx, lease, sourceID, "record", observed)
	return r, err
}

func (s *PgStore) RetireUndispatchedProjectEnvironmentClonePostgresCopyTarget(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	r, _, err := s.mutateClonePostgresCopyTarget(ctx, lease, sourceID, "retire", ProjectEnvironmentClonePostgresCopyTargetObservation{})
	return r, err
}
