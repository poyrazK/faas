package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/checkpointselection"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentClonePostgresCheckpointSelectionStore = (*PgStore)(nil)

func cloneCheckpointSelectionScopeTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, sourceID string) (checkpointselection.Scope, error) {
	op, fence, err := clonePostgresMaintenanceTx(ctx, tx, l, sourceID)
	if err != nil {
		return checkpointselection.Scope{}, err
	}
	m, err := readClonePostgresMaintenanceTx(ctx, tx, fence)
	if err != nil {
		return checkpointselection.Scope{}, err
	}
	if m.State != "ready" {
		return checkpointselection.Scope{}, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return checkpointselection.Scope{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return checkpointselection.Scope{}, err
	}
	source, err := capturedCloneWriteFenceDatabase(views, sourceID)
	if err != nil {
		return checkpointselection.Scope{}, err
	}
	hash, err := ProjectEnvironmentCloneDatabaseSourceHash(source)
	if err != nil {
		return checkpointselection.Scope{}, err
	}
	if hash != fence.SourceVersion {
		return checkpointselection.Scope{}, ErrConflict
	}
	// The lifecycle row is already locked. Retain the frozen major as well as
	// backend/dataset pins, refusing a changed live major before private IO.
	live, err := sqlc.New().ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(sourceID)})
	if err != nil {
		return checkpointselection.Scope{}, mapErr(err)
	}
	if int(live.PostgresMajor) != source.PostgresMajor {
		return checkpointselection.Scope{}, ErrConflict
	}
	scope := checkpointselection.Scope{OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceDatabaseID: sourceID, MaintenanceID: m.ID,
		SourceVersion: fence.SourceVersion, BackendID: fence.BackendID, BackendFingerprint: fence.BackendFingerprint, SourceProviderResourceID: fence.SourceProviderResourceID,
		SourceDataResourceID: fence.SourceDataResourceID, PostgresMajor: source.PostgresMajor, MaintenanceOwnerOID: m.OwnerOID, MaintenanceDatabaseOID: m.DatabaseOID}
	if scope.Validate() != nil {
		return checkpointselection.Scope{}, ErrConflict
	}
	return scope, nil
}

func readCloneCheckpointSelectionTx(ctx context.Context, tx pgx.Tx, scope checkpointselection.Scope) (ProjectEnvironmentClonePostgresCheckpointSelection, error) {
	r, err := sqlc.New().ReadClonePostgresCheckpointSelection(ctx, tx, sqlc.ReadClonePostgresCheckpointSelectionParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, mapErr(err)
	}
	var stored checkpointselection.Scope
	decoder := json.NewDecoder(bytes.NewReader(r.Scope))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&stored) != nil || decoder.Decode(new(any)) != io.EOF || stored != scope || pgUUIDString(r.MaintenanceID) != scope.MaintenanceID || !r.RetainedAt.Valid || r.RetainedAt.Time.IsZero() {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, ErrConflict
	}
	sealed := checkpointselection.Sealed{Scope: stored, Fingerprint: r.Fingerprint, KeyID: r.KeyID, CiphertextSHA256: r.CiphertextSha256, Ciphertext: bytes.Clone(r.Ciphertext)}
	if sealed.ValidateMetadata() != nil {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, ErrConflict
	}
	return ProjectEnvironmentClonePostgresCheckpointSelection{Sealed: sealed, RetainedAt: r.RetainedAt.Time}, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresCheckpointSelectionScopeForLease(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string) (checkpointselection.Scope, error) {
	if !validClonePostgresFenceLease(l) || !validCloneCredentialSourceID(sourceID) {
		return checkpointselection.Scope{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return checkpointselection.Scope{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := cloneCheckpointSelectionScopeTx(ctx, tx, l, sourceID)
	if err == nil {
		err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return checkpointselection.Scope{}, mapErr(err)
	}
	return scope, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresCheckpointSelectionForLease(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresCheckpointSelection, error) {
	if !validClonePostgresFenceLease(l) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := cloneCheckpointSelectionScopeTx(ctx, tx, l, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, err
	}
	r, err := readCloneCheckpointSelectionTx(ctx, tx, scope)
	if err == nil {
		err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return ProjectEnvironmentClonePostgresCheckpointSelection{}, mapErr(err)
	}
	return r, nil
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresCheckpointSelection(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, sealed checkpointselection.Sealed) (ProjectEnvironmentClonePostgresCheckpointSelection, bool, error) {
	var zero ProjectEnvironmentClonePostgresCheckpointSelection
	if !validClonePostgresFenceLease(l) || !validCloneCredentialSourceID(sourceID) {
		return zero, false, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, pgerrors.ErrQuotaExceeded) {
			return zero, false, ErrQuotaExceeded
		}
		return zero, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := cloneCheckpointSelectionScopeTx(ctx, tx, l, sourceID)
	if err != nil {
		return zero, false, err
	}
	if l.Operation.Status != CloneOperationCapturing || sealed.Scope != scope {
		return zero, false, ErrConflict
	}
	r, err := readCloneCheckpointSelectionTx(ctx, tx, scope)
	created := errors.Is(err, ErrNotFound)
	if created {
		raw, marshalErr := json.Marshal(scope)
		if marshalErr != nil {
			return zero, false, ErrConflict
		}
		_, err = sqlc.New().InsertClonePostgresCheckpointSelection(ctx, tx, sqlc.InsertClonePostgresCheckpointSelectionParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID), MaintenanceID: mustPgUUID(scope.MaintenanceID), Scope: raw,
			Fingerprint: sealed.Fingerprint, KeyID: sealed.KeyID, CiphertextSha256: sealed.CiphertextSHA256, Ciphertext: bytes.Clone(sealed.Ciphertext), ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrConflict
		}
		if err == nil {
			r, err = readCloneCheckpointSelectionTx(ctx, tx, scope)
		}
	} else if err == nil && (r.Sealed.Fingerprint != sealed.Fingerprint || r.Sealed.KeyID != sealed.KeyID || r.Sealed.CiphertextSHA256 != sealed.CiphertextSHA256 || !bytes.Equal(r.Sealed.Ciphertext, sealed.Ciphertext)) {
		err = ErrConflict
	}
	if err == nil {
		err = authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l)
	}
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err != nil {
		return zero, false, mapErr(err)
	}
	return r, created, nil
}
