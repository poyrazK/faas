package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresArchiveKey(operationID, ownerID string) string {
	return "postgres-copies/" + operationID + "/" + ownerID + ".age"
}

func validClonePostgresArchiveRequest(r ProjectEnvironmentClonePostgresArchiveRequest, limits ProjectEnvironmentClonePostgresArchiveLimits) bool {
	recipient, err := age.ParseX25519Recipient(r.KeyID)
	return r.Scope.Validate() == nil && r.DatabaseOID != 0 && validCloneObjectSHA256(r.InventoryFingerprint) && err == nil && recipient.String() == r.KeyID &&
		validCloneSnapshotID(r.StorageID) && validCloneObjectSHA256(r.StorageFingerprint) && r.ReservedBytes > 0 && r.ReservedBytes <= api.PostgresCopyArchiveCiphertextMaxBytes &&
		limits.Count > 0 && limits.Count <= api.PostgresCopyArchivesPerAccountMax && limits.Bytes > 0 && limits.Bytes <= api.PostgresCopyArchiveBytesPerAccountMax
}

func decodeCloneArchiveScope(raw []byte) (copyinventory.Scope, error) {
	var scope copyinventory.Scope
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&scope) != nil || d.Decode(new(any)) != io.EOF || scope.Validate() != nil {
		return scope, ErrConflict
	}
	return scope, nil
}

// Recovery binds retained inventory and the operation without requiring a live
// capture or current source configuration. New reservations authenticate the
// native capture separately before creating an owner.
func clonePostgresArchiveContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (copyinventory.Scope, ProjectEnvironmentClonePostgresInventory, error) {
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil {
		return copyinventory.Scope{}, ProjectEnvironmentClonePostgresInventory{}, err
	}
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresInventory(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresInventoryParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID)})
	if err != nil {
		return copyinventory.Scope{}, ProjectEnvironmentClonePostgresInventory{}, mapErr(err)
	}
	scope, err := decodeCloneArchiveScope(r.Scope)
	if err != nil || scope.OperationID != op.ID || scope.AccountID != op.AccountID || scope.ProjectID != op.ProjectID || scope.SourceDatabaseID != sourceID {
		return scope, ProjectEnvironmentClonePostgresInventory{}, ErrConflict
	}
	inventory, err := readClonePostgresInventoryTx(ctx, tx, scope)
	return scope, inventory, err
}

func readClonePostgresArchiveTx(ctx context.Context, tx pgx.Tx, scope copyinventory.Scope, fingerprint string, oid uint32) (ProjectEnvironmentClonePostgresArchive, error) {
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresArchive(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresArchiveParams{
		OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID), DatabaseOid: int64(oid)})
	if err != nil {
		return ProjectEnvironmentClonePostgresArchive{}, mapErr(err)
	}
	stored, err := decodeCloneArchiveScope(r.Scope)
	owner := pgUUIDString(r.OwnerID)
	request := ProjectEnvironmentClonePostgresArchiveRequest{Scope: stored, DatabaseOID: oid, InventoryFingerprint: r.InventoryFingerprint, KeyID: r.KeyID,
		StorageID: r.StorageID, StorageFingerprint: r.StorageFingerprint, ReservedBytes: r.ReservedBytes}
	if err != nil || !stored.Equal(scope) || r.InventoryFingerprint != fingerprint || pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID ||
		!validCloneCredentialSourceID(owner) || owner == scope.OperationID || owner == scope.SourceDatabaseID || owner == scope.CaptureDatabaseID ||
		r.StorageKey != clonePostgresArchiveKey(scope.OperationID, owner) || !r.CreatedAt.Valid || r.CreatedAt.Time.Before(scope.CaptureCreatedAt) ||
		!validClonePostgresArchiveRequest(request, ProjectEnvironmentClonePostgresArchiveLimits{Count: api.PostgresCopyArchivesPerAccountMax, Bytes: api.PostgresCopyArchiveBytesPerAccountMax}) {
		return ProjectEnvironmentClonePostgresArchive{}, ErrConflict
	}
	a := ProjectEnvironmentClonePostgresArchive{Scope: stored, DatabaseOID: oid, OwnerID: owner, State: r.State, InventoryFingerprint: r.InventoryFingerprint,
		KeyID: r.KeyID, StorageID: r.StorageID, StorageFingerprint: r.StorageFingerprint, StorageKey: r.StorageKey, ReservedBytes: r.ReservedBytes,
		CreatedAt: r.CreatedAt.Time, UploadStartedAt: r.UploadStartedAt.Time, RetainedAt: r.RetainedAt.Time}
	if a.State == "retained" {
		a.Receipt = copyarchive.Receipt{Scope: stored, InventoryFingerprint: fingerprint, SourceDatabaseOID: oid, PlainBytes: r.PlaintextBytes.Int64, CiphertextBytes: r.CiphertextBytes.Int64, CiphertextSHA256: r.CiphertextSha256.String}
		if !validCloneArchiveReceipt(a, a.Receipt) {
			return a, ErrConflict
		}
	}
	return a, nil
}

func validCloneArchiveReceipt(a ProjectEnvironmentClonePostgresArchive, r copyarchive.Receipt) bool {
	return a.Scope.Equal(r.Scope) && r.InventoryFingerprint == a.InventoryFingerprint && r.SourceDatabaseOID == a.DatabaseOID &&
		r.PlainBytes >= 5 && r.PlainBytes <= api.PostgresCopyArchiveMaxBytes && r.CiphertextBytes > r.PlainBytes && r.CiphertextBytes <= a.ReservedBytes && validCloneObjectSHA256(r.CiphertextSHA256)
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresArchive(ctx context.Context, lease ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresArchiveRequest, limits ProjectEnvironmentClonePostgresArchiveLimits) (ProjectEnvironmentClonePostgresArchive, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validClonePostgresArchiveRequest(request, limits) {
		return ProjectEnvironmentClonePostgresArchive{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresArchive{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, lease)
	if err != nil || op.Status != CloneOperationCapturing {
		if err == nil {
			err = ErrConflict
		}
		return ProjectEnvironmentClonePostgresArchive{}, false, err
	}
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return ProjectEnvironmentClonePostgresArchive{}, false, mapErr(err)
	}
	scope, inventory, err := clonePostgresArchiveContextTx(ctx, tx, lease, request.Scope.SourceDatabaseID)
	if err != nil {
		return ProjectEnvironmentClonePostgresArchive{}, false, err
	}
	if !scope.Equal(request.Scope) || inventory.Sealed.Fingerprint != request.InventoryFingerprint {
		return ProjectEnvironmentClonePostgresArchive{}, false, ErrConflict
	}
	a, err := readClonePostgresArchiveTx(ctx, tx, scope, request.InventoryFingerprint, request.DatabaseOID)
	created := errors.Is(err, ErrNotFound)
	if created {
		authenticated, authErr := clonePostgresInventoryScopeTx(ctx, tx, lease, scope.SourceDatabaseID)
		if authErr != nil || !authenticated.Equal(scope) {
			if authErr == nil {
				authErr = ErrConflict
			}
			return a, false, authErr
		}
		usage, countErr := q.CountProjectEnvironmentClonePostgresArchives(ctx, tx, mustPgUUID(scope.AccountID))
		if countErr != nil {
			return a, false, mapErr(countErr)
		}
		if usage.Count >= int64(limits.Count) || request.ReservedBytes > limits.Bytes || usage.Bytes > limits.Bytes-request.ReservedBytes {
			return a, false, ErrQuotaExceeded
		}
		owner := uuid.NewString()
		raw, _ := json.Marshal(scope)
		_, err = q.InsertProjectEnvironmentClonePostgresArchive(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresArchiveParams{
			OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID), DatabaseOid: int64(request.DatabaseOID),
			AccountID: mustPgUUID(scope.AccountID), ProjectID: mustPgUUID(scope.ProjectID), OwnerID: mustPgUUID(owner), Scope: raw, InventoryFingerprint: request.InventoryFingerprint,
			KeyID: request.KeyID, StorageID: request.StorageID, StorageFingerprint: request.StorageFingerprint, StorageKey: clonePostgresArchiveKey(scope.OperationID, owner), ReservedBytes: request.ReservedBytes,
			ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if err != nil {
			return a, false, cloneCopyReaderMutationError(err)
		}
		a, err = readClonePostgresArchiveTx(ctx, tx, scope, request.InventoryFingerprint, request.DatabaseOID)
	}
	if err != nil {
		return a, false, err
	}
	if a.KeyID != request.KeyID || a.StorageID != request.StorageID || a.StorageFingerprint != request.StorageFingerprint || a.ReservedBytes != request.ReservedBytes {
		return a, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return a, false, err
	}
	return a, created, mapErr(tx.Commit(ctx))
}

func (s *PgStore) mutateClonePostgresArchive(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, oid uint32, action string, receipt copyarchive.Receipt) (ProjectEnvironmentClonePostgresArchive, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) || oid == 0 {
		return ProjectEnvironmentClonePostgresArchive{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresArchive{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	scope, inventory, err := clonePostgresArchiveContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresArchive{}, false, err
	}
	a, err := readClonePostgresArchiveTx(ctx, tx, scope, inventory.Sealed.Fingerprint, oid)
	if err != nil {
		return a, false, err
	}
	q, dispatch := new(sqlc.Queries), false
	if action != "read" && lease.Operation.Status != CloneOperationCapturing {
		return a, false, ErrConflict
	}
	switch action {
	case "read":
	case "claim":
		if a.State == "reserved" {
			_, err = q.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, tx, sqlc.ClaimProjectEnvironmentClonePostgresArchiveUploadParams{
				OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(sourceID), DatabaseOid: int64(oid), ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
			dispatch = true
		}
	case "record":
		if !validCloneArchiveReceipt(a, receipt) || a.State == "reserved" {
			return a, false, ErrConflict
		}
		if a.State == "retained" {
			if a.Receipt.PlainBytes != receipt.PlainBytes || a.Receipt.CiphertextBytes != receipt.CiphertextBytes || a.Receipt.CiphertextSHA256 != receipt.CiphertextSHA256 {
				return a, false, ErrConflict
			}
		} else {
			_, err = q.RecordProjectEnvironmentClonePostgresArchive(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresArchiveParams{
				OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(sourceID), DatabaseOid: int64(oid), PlaintextBytes: receipt.PlainBytes,
				CiphertextBytes: receipt.CiphertextBytes, CiphertextSha256: receipt.CiphertextSHA256, ExpectedRevision: lease.Operation.Revision, WorkerToken: lease.Token})
		}
	default:
		return a, false, ErrInvalidArgument
	}
	if err != nil {
		return a, false, cloneCopyReaderMutationError(err)
	}
	a, err = readClonePostgresArchiveTx(ctx, tx, scope, inventory.Sealed.Fingerprint, oid)
	if err != nil {
		return a, false, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, lease.Operation, &lease); err != nil {
		return a, false, err
	}
	return a, dispatch, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresArchiveForLease(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, oid uint32) (ProjectEnvironmentClonePostgresArchive, error) {
	a, _, err := s.mutateClonePostgresArchive(ctx, l, sourceID, oid, "read", copyarchive.Receipt{})
	return a, err
}

func (s *PgStore) ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, oid uint32) (ProjectEnvironmentClonePostgresArchive, bool, error) {
	return s.mutateClonePostgresArchive(ctx, l, sourceID, oid, "claim", copyarchive.Receipt{})
}

// Callers independently read back the exact frozen key and fully authenticate
// its ciphertext/header/payload before recording. Metadata alone cannot prove IO.
func (s *PgStore) RecordProjectEnvironmentClonePostgresArchive(ctx context.Context, l ProjectEnvironmentCloneLease, sourceID string, oid uint32, receipt copyarchive.Receipt) (ProjectEnvironmentClonePostgresArchive, error) {
	a, _, err := s.mutateClonePostgresArchive(ctx, l, sourceID, oid, "record", receipt)
	return a, err
}
