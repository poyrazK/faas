package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type cloneContentsParents struct {
	inventory ProjectEnvironmentClonePostgresInventory
	archive   ProjectEnvironmentClonePostgresArchive
	reader    ProjectEnvironmentClonePostgresCopyReader
}

// Retained parent identities remain readable after authenticated input cleanup.
// Creating/capturing an owner separately requires live native/reader authority.
func cloneContentsContextTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, id string, oid uint32) (cloneContentsParents, error) {
	scope, inventory, err := clonePostgresArchiveContextTx(ctx, tx, l, id)
	if err != nil {
		return cloneContentsParents{}, err
	}
	a, err := readClonePostgresArchiveTx(ctx, tx, scope, inventory.Sealed.Fingerprint, oid)
	if err != nil {
		return cloneContentsParents{}, err
	}
	r, err := readCloneCopyReaderTx(ctx, tx, scope)
	if err != nil {
		return cloneContentsParents{}, err
	}
	if r.EndpointID == "" || r.RequestStartedAt.IsZero() || r.EndpointCreatedAt.IsZero() {
		return cloneContentsParents{}, ErrConflict
	}
	return cloneContentsParents{inventory, a, r}, nil
}

func (p cloneContentsParents) authenticate(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease) error {
	scope, err := clonePostgresInventoryScopeTx(ctx, tx, l, p.inventory.Sealed.Scope.SourceDatabaseID)
	if err != nil {
		return err
	}
	if l.Operation.Status != CloneOperationCapturing || !scope.Equal(p.inventory.Sealed.Scope) || p.reader.State != "observed" || !p.reader.Available {
		return ErrConflict
	}
	return nil
}

func readCloneContentsTx(ctx context.Context, tx pgx.Tx, p cloneContentsParents) (ProjectEnvironmentClonePostgresContents, error) {
	scope := p.inventory.Sealed.Scope
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresContents(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresContentsParams{
		OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID), DatabaseOid: int64(p.archive.DatabaseOID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, mapErr(err)
	}
	stored, err := decodeCloneArchiveScope(r.Scope)
	a := ProjectEnvironmentClonePostgresContents{Scope: stored, DatabaseOID: uint32(r.DatabaseOid), OwnerID: pgUUIDString(r.OwnerID), State: r.State, KeyID: r.KeyID,
		InventoryFingerprint: r.InventoryFingerprint, InventoryCiphertextSHA256: r.InventoryCiphertextSha256,
		ArchiveOwnerID: pgUUIDString(r.ArchiveOwnerID), ArchiveReservationSHA256: r.ArchiveReservationSha256,
		ReaderOwnerID: pgUUIDString(r.ReaderOwnerID), ReaderIdentitySHA256: r.ReaderIdentitySha256, ReservedBytes: r.ReservedBytes,
		CreatedAt: r.CreatedAt.Time, CapturedAt: r.CapturedAt.Time}
	key, keyErr := age.ParseX25519Recipient(a.KeyID)
	if err != nil || keyErr != nil || key.String() != a.KeyID || !stored.Equal(scope) || a.DatabaseOID != p.archive.DatabaseOID ||
		!validCloneCredentialSourceID(a.OwnerID) || a.OwnerID == scope.OperationID || a.OwnerID == scope.SourceDatabaseID || a.OwnerID == scope.CaptureDatabaseID || a.OwnerID == a.ArchiveOwnerID || a.OwnerID == a.ReaderOwnerID ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || a.InventoryFingerprint != p.inventory.Sealed.Fingerprint || a.InventoryCiphertextSHA256 != p.inventory.Sealed.CiphertextSHA256 ||
		a.ArchiveOwnerID != p.archive.OwnerID || a.ArchiveReservationSHA256 != p.archive.ReservationFingerprint() || a.ReaderOwnerID != p.reader.OwnerID || a.ReaderIdentitySHA256 != p.reader.IdentityFingerprint() ||
		a.ReservedBytes < 1 || a.ReservedBytes > api.PostgresCopyCiphertextMaxBytes || !r.CreatedAt.Valid || a.CreatedAt.Before(p.inventory.CapturedAt) || a.CreatedAt.Before(p.archive.CreatedAt) || a.CreatedAt.Before(p.reader.EndpointCreatedAt) {
		return ProjectEnvironmentClonePostgresContents{}, ErrConflict
	}
	if a.State == "captured" {
		a.Sealed = copycontents.Sealed{Scope: stored, SourceDatabaseOID: a.DatabaseOID, InventoryFingerprint: a.InventoryFingerprint, Fingerprint: r.Fingerprint.String,
			KeyID: a.KeyID, CiphertextSHA256: r.CiphertextSha256.String, Ciphertext: bytes.Clone(r.Ciphertext)}
		if !r.CapturedAt.Valid || a.CapturedAt.Before(a.CreatedAt) || int64(len(a.Sealed.Ciphertext)) > a.ReservedBytes || a.Sealed.ValidateMetadata() != nil {
			return ProjectEnvironmentClonePostgresContents{}, ErrConflict
		}
	} else if a.State != "reserved" || r.CapturedAt.Valid || r.Fingerprint.Valid || r.CiphertextSha256.Valid || r.Ciphertext != nil {
		return ProjectEnvironmentClonePostgresContents{}, ErrConflict
	}
	return a, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresContentsForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32) (ProjectEnvironmentClonePostgresContents, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || oid == 0 {
		return ProjectEnvironmentClonePostgresContents{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	p, err := cloneContentsContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, err
	}
	a, err := readCloneContentsTx(ctx, tx, p)
	if err != nil {
		return a, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return ProjectEnvironmentClonePostgresContents{}, err
	}
	return a, mapErr(tx.Commit(ctx))
}

func validCloneContentsRequest(r ProjectEnvironmentClonePostgresContentsRequest, limits ProjectEnvironmentClonePostgresContentsLimits) bool {
	key, err := age.ParseX25519Recipient(r.KeyID)
	return r.Scope.Validate() == nil && r.DatabaseOID != 0 && validCloneObjectSHA256(r.InventoryFingerprint) && err == nil && key.String() == r.KeyID &&
		r.ReservedBytes > 0 && r.ReservedBytes <= api.PostgresCopyCiphertextMaxBytes && limits.Count > 0 && limits.Count <= api.PostgresCopyContentsManifestsPerAccountMax &&
		limits.Bytes > 0 && limits.Bytes <= api.PostgresCopyContentsBytesPerAccountMax
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresContents(ctx context.Context, l ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresContentsRequest, limits ProjectEnvironmentClonePostgresContentsLimits) (ProjectEnvironmentClonePostgresContents, bool, error) {
	if !validCloneLeaseIdentity(l) || !validCloneContentsRequest(request, limits) {
		return ProjectEnvironmentClonePostgresContents{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, l)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, false, err
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentClonePostgresContents{}, false, ErrConflict
	}
	q := new(sqlc.Queries)
	if _, err = q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return ProjectEnvironmentClonePostgresContents{}, false, mapErr(err)
	}
	p, err := cloneContentsContextTx(ctx, tx, l, request.Scope.SourceDatabaseID, request.DatabaseOID)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, false, err
	}
	if !request.Scope.Equal(p.inventory.Sealed.Scope) || request.InventoryFingerprint != p.inventory.Sealed.Fingerprint {
		return ProjectEnvironmentClonePostgresContents{}, false, ErrConflict
	}
	a, err := readCloneContentsTx(ctx, tx, p)
	created := errors.Is(err, ErrNotFound)
	if created {
		if err = p.authenticate(ctx, tx, l); err != nil {
			return ProjectEnvironmentClonePostgresContents{}, false, err
		}
		usage, countErr := q.CountProjectEnvironmentClonePostgresContents(ctx, tx, mustPgUUID(op.AccountID))
		if countErr != nil {
			return ProjectEnvironmentClonePostgresContents{}, false, mapErr(countErr)
		}
		if usage.Count >= int64(limits.Count) || request.ReservedBytes > limits.Bytes || usage.Bytes > limits.Bytes-request.ReservedBytes {
			return ProjectEnvironmentClonePostgresContents{}, false, ErrQuotaExceeded
		}
		raw, _ := json.Marshal(request.Scope)
		_, err = q.InsertProjectEnvironmentClonePostgresContents(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresContentsParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(request.Scope.SourceDatabaseID), DatabaseOid: int64(request.DatabaseOID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), OwnerID: mustPgUUID(uuid.NewString()), Scope: raw,
			InventoryFingerprint: request.InventoryFingerprint, InventoryCiphertextSha256: p.inventory.Sealed.CiphertextSHA256, ArchiveOwnerID: mustPgUUID(p.archive.OwnerID), ArchiveReservationSha256: p.archive.ReservationFingerprint(),
			ReaderOwnerID: mustPgUUID(p.reader.OwnerID), ReaderIdentitySha256: p.reader.IdentityFingerprint(), KeyID: request.KeyID, ReservedBytes: request.ReservedBytes, ExpectedRevision: op.Revision, WorkerToken: l.Token})
		if err != nil {
			return ProjectEnvironmentClonePostgresContents{}, false, cloneCopyReaderMutationError(err)
		}
		a, err = readCloneContentsTx(ctx, tx, p)
	}
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, false, err
	}
	if a.KeyID != request.KeyID || a.ReservedBytes != request.ReservedBytes {
		return ProjectEnvironmentClonePostgresContents{}, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &l); err != nil {
		return ProjectEnvironmentClonePostgresContents{}, false, err
	}
	return a, created, mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresContents(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, sealed copycontents.Sealed) (ProjectEnvironmentClonePostgresContents, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || oid == 0 {
		return ProjectEnvironmentClonePostgresContents{}, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, pgerrors.ErrQuotaExceeded) {
			return ProjectEnvironmentClonePostgresContents{}, ErrQuotaExceeded
		}
		return ProjectEnvironmentClonePostgresContents{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	p, err := cloneContentsContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, err
	}
	a, err := readCloneContentsTx(ctx, tx, p)
	if err != nil {
		return ProjectEnvironmentClonePostgresContents{}, err
	}
	if l.Operation.Status != CloneOperationCapturing || !sealed.Scope.Equal(a.Scope) || sealed.SourceDatabaseOID != oid || sealed.InventoryFingerprint != a.InventoryFingerprint || sealed.KeyID != a.KeyID {
		return ProjectEnvironmentClonePostgresContents{}, ErrConflict
	}
	if int64(len(sealed.Ciphertext)) > a.ReservedBytes {
		return ProjectEnvironmentClonePostgresContents{}, ErrQuotaExceeded
	}
	if a.State == "reserved" {
		if err = p.authenticate(ctx, tx, l); err != nil {
			return ProjectEnvironmentClonePostgresContents{}, err
		}
		_, err = new(sqlc.Queries).RecordProjectEnvironmentClonePostgresContents(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresContentsParams{
			OperationID: mustPgUUID(a.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), Fingerprint: sealed.Fingerprint,
			Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return ProjectEnvironmentClonePostgresContents{}, cloneCopyReaderMutationError(err)
		}
		a, err = readCloneContentsTx(ctx, tx, p)
		if err != nil {
			return ProjectEnvironmentClonePostgresContents{}, err
		}
	}
	if a.Sealed.Fingerprint != sealed.Fingerprint || a.Sealed.CiphertextSHA256 != sealed.CiphertextSHA256 || !bytes.Equal(a.Sealed.Ciphertext, sealed.Ciphertext) {
		return ProjectEnvironmentClonePostgresContents{}, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return ProjectEnvironmentClonePostgresContents{}, err
	}
	return a, mapErr(tx.Commit(ctx))
}
