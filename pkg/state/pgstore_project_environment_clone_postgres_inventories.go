package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresInventoryScopeTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (copyinventory.Scope, error) {
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	if err != nil {
		return copyinventory.Scope{}, err
	}
	capture, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err != nil {
		return copyinventory.Scope{}, err
	}
	if capture.AdoptedDatabaseID == "" || capture.AdoptedDatabaseID != capture.TargetOwnerID || capture.AdoptedAt.IsZero() ||
		op.Status != CloneOperationCompensating && capture.State != "adopted" {
		return copyinventory.Scope{}, ErrConflict
	}
	// The adopted catalogue was already checked against the frozen definition
	// under this row lock. Bind its major too; SQL from a different version
	// cannot become the durable inventory for this capture.
	database, err := new(sqlc.Queries).LockManagedPostgresLifecycleDatabase(ctx, tx, sqlc.LockManagedPostgresLifecycleDatabaseParams{
		AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(capture.AdoptedDatabaseID)})
	if err != nil {
		return copyinventory.Scope{}, mapErr(err)
	}
	scope := copyinventory.Scope{PostgresMajor: int(database.PostgresMajor), OperationID: op.ID, AccountID: op.AccountID, ProjectID: op.ProjectID, SourceDatabaseID: snapshot.SourceDatabaseID,
		CaptureDatabaseID: capture.AdoptedDatabaseID, SourceVersion: snapshot.SourceVersion, BackendID: snapshot.BackendID, BackendFingerprint: snapshot.BackendFingerprint,
		SourceProviderResourceID: snapshot.SourceProviderResourceID, SourceDataResourceID: snapshot.SourceDataResourceID, ProviderSnapshotID: snapshot.ProviderSnapshotID,
		CaptureProviderResourceID: capture.TargetProviderResourceID, CapturePoint: snapshot.CapturePoint, SnapshotCreatedAt: snapshot.SnapshotCreatedAt, CaptureCreatedAt: capture.TargetCreatedAt}
	if scope.Validate() != nil {
		return scope, ErrConflict
	}
	return scope, nil
}

func readClonePostgresInventoryTx(ctx context.Context, tx pgx.Tx, scope copyinventory.Scope) (ProjectEnvironmentClonePostgresInventory, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresInventory(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresInventoryParams{
		OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresInventory{}, mapErr(err)
	}
	var stored copyinventory.Scope
	decoder := json.NewDecoder(bytes.NewReader(r.Scope))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&stored) != nil || decoder.Decode(new(any)) != io.EOF || !scope.Equal(stored) ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || pgUUIDString(r.CaptureDatabaseID) != scope.CaptureDatabaseID {
		return ProjectEnvironmentClonePostgresInventory{}, ErrConflict
	}
	sealed := copyinventory.Sealed{Scope: stored, Fingerprint: r.Fingerprint, KeyID: r.KeyID, CiphertextSHA256: r.CiphertextSha256, Ciphertext: bytes.Clone(r.Ciphertext)}
	if sealed.ValidateMetadata() != nil || !r.CapturedAt.Valid || r.CapturedAt.Time.Before(scope.CaptureCreatedAt) {
		return ProjectEnvironmentClonePostgresInventory{}, ErrConflict
	}
	return ProjectEnvironmentClonePostgresInventory{Sealed: sealed, CapturedAt: r.CapturedAt.Time}, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresInventoryScopeForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (copyinventory.Scope, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return copyinventory.Scope{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return copyinventory.Scope{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := clonePostgresInventoryScopeTx(ctx, tx, lease, sourceID)
	if err != nil {
		return scope, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, &lease); err != nil {
		return scope, err
	}
	return scope, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresInventoryForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresInventory, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresInventory{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresInventory{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := clonePostgresInventoryScopeTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresInventory{}, err
	}
	receipt, err := readClonePostgresInventoryTx(ctx, tx, scope)
	if err != nil {
		return receipt, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresInventory(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, sealed copyinventory.Sealed) (ProjectEnvironmentClonePostgresInventory, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresInventory{}, false, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, managedpostgres.ErrQuotaExceeded) {
			return ProjectEnvironmentClonePostgresInventory{}, false, ErrQuotaExceeded
		}
		return ProjectEnvironmentClonePostgresInventory{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresInventory{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, err := clonePostgresInventoryScopeTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresInventory{}, false, err
	}
	if lease.Operation.Status != CloneOperationCapturing || !scope.Equal(sealed.Scope) {
		return ProjectEnvironmentClonePostgresInventory{}, false, ErrConflict
	}
	receipt, err := readClonePostgresInventoryTx(ctx, tx, scope)
	created := errors.Is(err, ErrNotFound)
	if created {
		rawScope, marshalErr := json.Marshal(scope)
		if marshalErr != nil {
			return receipt, false, ErrConflict
		}
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresInventory(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresInventoryParams{
			OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID), AccountID: mustPgUUID(scope.AccountID), ProjectID: mustPgUUID(scope.ProjectID),
			CaptureDatabaseID: mustPgUUID(scope.CaptureDatabaseID), Scope: rawScope, Fingerprint: sealed.Fingerprint, KeyID: sealed.KeyID,
			Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrConflict
		}
		if err == nil {
			receipt, err = readClonePostgresInventoryTx(ctx, tx, scope)
		}
	}
	if err != nil {
		return receipt, false, mapErr(err)
	}
	if receipt.Sealed.Fingerprint != sealed.Fingerprint || receipt.Sealed.KeyID != sealed.KeyID || receipt.Sealed.CiphertextSHA256 != sealed.CiphertextSHA256 ||
		!bytes.Equal(receipt.Sealed.Ciphertext, sealed.Ciphertext) {
		return receipt, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, &lease); err != nil {
		return receipt, false, err
	}
	return receipt, created, mapErr(tx.Commit(ctx))
}
