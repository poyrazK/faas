package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cloneCopyReaderOperationIDs(raw string) ([]string, error) {
	var ids []string
	if json.Unmarshal([]byte(raw), &ids) != nil {
		return nil, ErrConflict
	}
	_, ids, err := canonicalCloneCopyReaderOperationIDs(ids)
	return ids, err
}

func canonicalCloneCopyReaderOperationIDs(ids []string) ([]byte, []string, error) {
	if len(ids) > api.PostgresCopyReaderMaxOperations {
		return nil, nil, ErrQuotaExceeded
	}
	return canonicalCloneForkDeletionOperations(ids)
}

func (r ProjectEnvironmentClonePostgresCopyReader) DeletionOperationIDs() ([]string, error) {
	return cloneCopyReaderOperationIDs(r.DeletionOperations)
}

func (r ProjectEnvironmentClonePostgresCopyReader) CaptureDeletionOperationIDs() ([]string, error) {
	return cloneCopyReaderOperationIDs(r.CaptureDeletionOperations)
}

func readCloneCopyReaderTx(ctx context.Context, tx pgx.Tx, scope copyinventory.Scope) (ProjectEnvironmentClonePostgresCopyReader, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresCopyReader(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresCopyReaderParams{
		OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyReader{}, mapErr(err)
	}
	var stored copyinventory.Scope
	d := json.NewDecoder(bytes.NewReader(r.Scope))
	d.DisallowUnknownFields()
	owner := pgUUIDString(r.OwnerID)
	if d.Decode(&stored) != nil || d.Decode(new(any)) != io.EOF || !scope.Equal(stored) ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || pgUUIDString(r.CaptureDatabaseID) != scope.CaptureDatabaseID ||
		!validCloneCredentialSourceID(owner) || owner == scope.CaptureDatabaseID || owner == scope.SourceDatabaseID || owner == scope.OperationID ||
		!r.CreatedAt.Valid || r.CreatedAt.Time.Before(scope.CaptureCreatedAt) {
		return ProjectEnvironmentClonePostgresCopyReader{}, ErrConflict
	}
	actual := ProjectEnvironmentClonePostgresCopyReader{Scope: stored, OwnerID: owner, State: r.State, EndpointID: r.EndpointID.String,
		RequestStartedAt: r.RequestStartedAt.Time, EndpointCreatedAt: r.EndpointCreatedAt.Time, ObservedAt: r.ObservedAt.Time, Available: r.Available,
		CleanupRequestedAt: r.CleanupRequestedAt.Time, CleanupDispatchedAt: r.CleanupDispatchedAt.Time, RetiredAt: r.RetiredAt.Time,
		DeletionOperations: string(r.DeleteOperationIds), CaptureDeletionOperations: string(r.CaptureOperationIds)}
	if actual.EndpointID != "" && (validateCloneCopyReaderObservation(actual, ProjectEnvironmentClonePostgresCopyReaderObservation{
		EndpointID: actual.EndpointID, CreatedAt: actual.EndpointCreatedAt}) != nil || actual.ObservedAt.Before(actual.EndpointCreatedAt)) {
		return actual, ErrConflict
	}
	if _, err := actual.DeletionOperationIDs(); err != nil {
		return actual, err
	}
	if _, err := actual.CaptureDeletionOperationIDs(); err != nil {
		return actual, err
	}
	return actual, nil
}

func validateCloneCopyReaderObservation(r ProjectEnvironmentClonePostgresCopyReader, o ProjectEnvironmentClonePostgresCopyReaderObservation) error {
	if r.RequestStartedAt.IsZero() || !validCloneSnapshotID(o.EndpointID) || o.EndpointID == r.Scope.SourceProviderResourceID ||
		o.EndpointID == r.Scope.SourceDataResourceID || o.EndpointID == r.Scope.CaptureProviderResourceID ||
		o.CreatedAt.IsZero() || o.CreatedAt.Before(r.RequestStartedAt) || o.CreatedAt.Nanosecond()%1000 != 0 ||
		r.EndpointID != "" && (o.EndpointID != r.EndpointID || !o.CreatedAt.Equal(r.EndpointCreatedAt)) {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresCopyReader(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, limit int) (ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) || limit < 1 || limit > api.PostgresCopyReadersPerAccountMax {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil || op.Status != CloneOperationCapturing {
		if err == nil {
			err = ErrConflict
		}
		return ProjectEnvironmentClonePostgresCopyReader{}, false, err
	}
	q := new(sqlc.Queries)
	// Use the same account lock order as the capture/database reservations.
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, mapErr(err)
	}
	scope, err := clonePostgresInventoryScopeTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, err
	}
	r, err := readCloneCopyReaderTx(ctx, tx, scope)
	created := errors.Is(err, ErrNotFound)
	if created {
		count, countErr := q.CountProjectEnvironmentClonePostgresCopyReaders(ctx, tx, mustPgUUID(scope.AccountID))
		if countErr != nil {
			return r, false, mapErr(countErr)
		}
		if count >= int64(limit) {
			return r, false, ErrQuotaExceeded
		}
		raw, _ := json.Marshal(scope)
		_, err = q.InsertProjectEnvironmentClonePostgresCopyReader(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresCopyReaderParams{
			OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(sourceID), AccountID: mustPgUUID(scope.AccountID), ProjectID: mustPgUUID(scope.ProjectID),
			CaptureDatabaseID: mustPgUUID(scope.CaptureDatabaseID), Scope: raw, OwnerID: mustPgUUID(uuid.NewString()), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if err != nil {
			return r, false, cloneCopyReaderMutationError(err)
		}
		r, err = readCloneCopyReaderTx(ctx, tx, scope)
	}
	if err != nil {
		return r, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, false, err
	}
	return r, created, mapErr(tx.Commit(ctx))
}

func cloneCopyReaderMutationError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) mutateCloneCopyReader(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID, action string, observed ProjectEnvironmentClonePostgresCopyReaderObservation, proof ProjectEnvironmentClonePostgresCopyReaderDeletion) (ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := clonePostgresInventoryScopeTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyReader{}, false, err
	}
	r, err := readCloneCopyReaderTx(ctx, tx, scope)
	if err != nil {
		return r, false, err
	}
	q, dispatch := new(sqlc.Queries), false
	op := lease.Operation
	switch action {
	case "read":
	case "claim":
		if op.Status != CloneOperationCapturing || r.State == "deleting" || r.State == "retired" {
			return r, false, ErrConflict
		}
		if r.State == "reserved" {
			_, err = q.ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresCopyReaderRequestParams{
				OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedStatus: string(op.Status), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
			dispatch = true
		}
	case "record":
		if op.Status != CloneOperationCapturing && op.Status != CloneOperationCompensating ||
			op.Status == CloneOperationCompensating && r.State != "deleting" || r.State == "reserved" || r.State == "retired" ||
			validateCloneCopyReaderObservation(r, observed) != nil {
			return r, false, ErrConflict
		}
		_, err = q.RecordProjectEnvironmentClonePostgresCopyReader(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresCopyReaderParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), EndpointID: observed.EndpointID,
			EndpointCreatedAt: pgtype.Timestamptz{Time: observed.CreatedAt, Valid: true}, Available: observed.Available,
			ExpectedStatus: string(op.Status), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
	case "begin", "delete_claim", "proof", "finish":
		if op.Status != CloneOperationCompensating {
			return r, false, ErrConflict
		}
		switch action {
		case "begin":
			if r.State != "deleting" && r.State != "retired" {
				_, err = q.BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, tx, sqlc.BeginProjectEnvironmentClonePostgresCopyReaderCleanupParams{
					OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedStatus: string(op.Status), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
			}
		case "delete_claim":
			if r.State != "deleting" || r.EndpointID == "" {
				return r, false, ErrConflict
			}
			if r.CleanupDispatchedAt.IsZero() {
				_, err = q.ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresCopyReaderCleanupParams{
					OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedStatus: string(op.Status), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
				dispatch = true
			}
		default:
			err = mutateCloneCopyReaderProofTx(ctx, tx, lease, r, proof, action == "finish")
		}
	default:
		return r, false, ErrInvalidArgument
	}
	if err != nil {
		return r, false, cloneCopyReaderMutationError(err)
	}
	r, err = readCloneCopyReaderTx(ctx, tx, scope)
	if err != nil {
		return r, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, false, err
	}
	return r, dispatch, mapErr(tx.Commit(ctx))
}

func mutateCloneCopyReaderProofTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, r ProjectEnvironmentClonePostgresCopyReader, proof ProjectEnvironmentClonePostgresCopyReaderDeletion, finish bool) error {
	epJSON, epIDs, err := canonicalCloneCopyReaderOperationIDs(proof.OperationIDs)
	if err != nil {
		return err
	}
	captureJSON, captureIDs, err := canonicalCloneCopyReaderOperationIDs(proof.CaptureOperationIDs)
	if err != nil {
		return err
	}
	stored, _ := r.DeletionOperationIDs()
	storedCapture, _ := r.CaptureDeletionOperationIDs()
	if r.State != "deleting" && r.State != "retired" || finish && !proof.Done ||
		proof.EndpointID != r.EndpointID || !proof.CreatedAt.Equal(r.EndpointCreatedAt) ||
		len(stored) > 0 && !slices.Equal(stored, epIDs) || len(storedCapture) > 0 && !slices.Equal(storedCapture, captureIDs) ||
		finish && (!slices.Equal(stored, epIDs) || !slices.Equal(storedCapture, captureIDs)) {
		return ErrConflict
	}
	if r.RequestStartedAt.IsZero() {
		if !finish || r.EndpointID != "" || !r.CleanupDispatchedAt.IsZero() || len(epIDs)+len(captureIDs) != 0 {
			return ErrConflict
		}
	} else if r.EndpointID == "" || len(epIDs) > 0 && r.CleanupDispatchedAt.IsZero() || finish && len(epIDs)+len(captureIDs) == 0 {
		return ErrConflict
	}
	q := new(sqlc.Queries)
	if len(captureIDs) > 0 {
		row, err := q.ReadProjectEnvironmentClonePostgresSnapshotRestore(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresSnapshotRestoreParams{
			OperationID: mustPgUUID(r.Scope.OperationID), SourceDatabaseID: mustPgUUID(r.Scope.SourceDatabaseID)})
		if err != nil {
			return err
		}
		capture := clonePostgresSnapshotRestoreFromSQL(row)
		ids, err := capture.DeletionOperationIDs()
		if err != nil || capture.State != "deleting" && capture.State != "deleted" || !slices.Equal(ids, captureIDs) {
			return ErrConflict
		}
	}
	if r.State == "retired" {
		return nil
	}
	if finish {
		_, err = q.FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresCopyReaderCleanupParams{
			OperationID: mustPgUUID(r.Scope.OperationID), SourceDatabaseID: mustPgUUID(r.Scope.SourceDatabaseID), ExpectedStatus: string(lease.Operation.Status), ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
	} else {
		_, err = q.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperationsParams{
			OperationID: mustPgUUID(r.Scope.OperationID), SourceDatabaseID: mustPgUUID(r.Scope.SourceDatabaseID), EndpointID: proof.EndpointID,
			EndpointCreatedAt: pgtype.Timestamptz{Time: proof.CreatedAt, Valid: true}, DeleteOperationIds: epJSON, CaptureOperationIds: captureJSON,
			ExpectedStatus: string(lease.Operation.Status), ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
	}
	return err
}

func (s *PgStore) ProjectEnvironmentClonePostgresCopyReaderForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresCopyReader, error) {
	r, _, err := s.mutateCloneCopyReader(ctx, l, id, "read", ProjectEnvironmentClonePostgresCopyReaderObservation{}, ProjectEnvironmentClonePostgresCopyReaderDeletion{})
	return r, err
}
func (s *PgStore) ClaimProjectEnvironmentClonePostgresCopyReaderRequest(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	return s.mutateCloneCopyReader(ctx, l, id, "claim", ProjectEnvironmentClonePostgresCopyReaderObservation{}, ProjectEnvironmentClonePostgresCopyReaderDeletion{})
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresCopyReader(ctx context.Context, l ProjectEnvironmentCloneLease, id string, o ProjectEnvironmentClonePostgresCopyReaderObservation) (ProjectEnvironmentClonePostgresCopyReader, error) {
	r, _, err := s.mutateCloneCopyReader(ctx, l, id, "record", o, ProjectEnvironmentClonePostgresCopyReaderDeletion{})
	return r, err
}
func (s *PgStore) BeginProjectEnvironmentClonePostgresCopyReaderCleanup(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresCopyReader, error) {
	r, _, err := s.mutateCloneCopyReader(ctx, l, id, "begin", ProjectEnvironmentClonePostgresCopyReaderObservation{}, ProjectEnvironmentClonePostgresCopyReaderDeletion{})
	return r, err
}
func (s *PgStore) ClaimProjectEnvironmentClonePostgresCopyReaderCleanup(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresCopyReader, bool, error) {
	return s.mutateCloneCopyReader(ctx, l, id, "delete_claim", ProjectEnvironmentClonePostgresCopyReaderObservation{}, ProjectEnvironmentClonePostgresCopyReaderDeletion{})
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresCopyReaderDeletionOperations(ctx context.Context, l ProjectEnvironmentCloneLease, id string, p ProjectEnvironmentClonePostgresCopyReaderDeletion) (ProjectEnvironmentClonePostgresCopyReader, error) {
	r, _, err := s.mutateCloneCopyReader(ctx, l, id, "proof", ProjectEnvironmentClonePostgresCopyReaderObservation{}, p)
	return r, err
}
func (s *PgStore) FinishProjectEnvironmentClonePostgresCopyReaderCleanup(ctx context.Context, l ProjectEnvironmentCloneLease, id string, p ProjectEnvironmentClonePostgresCopyReaderDeletion) (ProjectEnvironmentClonePostgresCopyReader, error) {
	r, _, err := s.mutateCloneCopyReader(ctx, l, id, "finish", ProjectEnvironmentClonePostgresCopyReaderObservation{}, p)
	return r, err
}
