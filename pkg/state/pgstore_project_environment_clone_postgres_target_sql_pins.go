package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresTargetSQLPinsContextTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresCopyTarget, ProjectEnvironmentClonePostgresInventory, error) {
	op, capture, source, resource, point, err := clonePostgresCopyContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, ProjectEnvironmentClonePostgresInventory{}, err
	}
	target, err := readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
	if err != nil {
		return target, ProjectEnvironmentClonePostgresInventory{}, err
	}
	scope, inventory, err := clonePostgresArchiveContextTx(ctx, tx, l, id)
	if err == nil && (target.CaptureDatabaseID != scope.CaptureDatabaseID || target.ProviderResourceID == "" || target.ProviderCreatedAt.Before(scope.CaptureCreatedAt)) {
		err = ErrConflict
	}
	return target, inventory, err
}

func readClonePostgresTargetSQLPinsTx(ctx context.Context, tx pgx.Tx, target ProjectEnvironmentClonePostgresCopyTarget, inventory ProjectEnvironmentClonePostgresInventory) (ProjectEnvironmentClonePostgresTargetSQLPins, error) {
	scope := inventory.Sealed.Scope
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresTargetSQLPins(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresTargetSQLPinsParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, mapErr(err)
	}
	stored, err := decodeCloneArchiveScope(r.Scope)
	sealed := copyarchive.SealedTarget{Scope: stored, OwnerID: pgUUIDString(r.TargetDatabaseID), ProviderResourceID: r.TargetProviderResourceID, ProviderCreatedAt: r.TargetProviderCreatedAt.Time,
		Fingerprint: r.TargetFingerprint, KeyID: r.KeyID, CiphertextSHA256: r.CiphertextSha256, Ciphertext: bytes.Clone(r.Ciphertext)}
	if err != nil || !stored.Equal(scope) || sealed.ValidateMetadata() != nil || sealed.OwnerID != target.TargetDatabaseID || sealed.ProviderResourceID != target.ProviderResourceID || !sealed.ProviderCreatedAt.Equal(target.ProviderCreatedAt) ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || r.InventoryFingerprint != inventory.Sealed.Fingerprint || !r.CapturedAt.Valid || !r.TargetProviderCreatedAt.Valid ||
		target.PreparedAt.IsZero() || r.CapturedAt.Time.Before(target.PreparedAt) || r.CapturedAt.Time.Before(inventory.CapturedAt) || r.CapturedAt.Time.Before(target.ProviderCreatedAt) {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, ErrConflict
	}
	return ProjectEnvironmentClonePostgresTargetSQLPins{Sealed: sealed, InventoryFingerprint: r.InventoryFingerprint, CapturedAt: r.CapturedAt.Time}, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresTargetSQLPinsForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresTargetSQLPins, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	target, inventory, err := clonePostgresTargetSQLPinsContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, err
	}
	r, err := readClonePostgresTargetSQLPinsTx(ctx, tx, target, inventory)
	if err != nil {
		return r, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

// First authenticated observation wins. Even an equivalent re-encryption cannot
// replace the committed bytes/key. Recovery opens the original receipt instead.
func (s *PgStore) RecordProjectEnvironmentClonePostgresTargetSQLPins(ctx context.Context, l ProjectEnvironmentCloneLease, id, fingerprint string, sealed copyarchive.SealedTarget) (ProjectEnvironmentClonePostgresTargetSQLPins, bool, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || !validCloneObjectSHA256(fingerprint) {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, false, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, pgerrors.ErrQuotaExceeded) {
			return ProjectEnvironmentClonePostgresTargetSQLPins{}, false, ErrQuotaExceeded
		}
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	target, inventory, err := clonePostgresTargetSQLPinsContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, false, err
	}
	scope := inventory.Sealed.Scope
	if l.Operation.Status != CloneOperationCapturing || target.State != "prepared" || fingerprint != inventory.Sealed.Fingerprint || !scope.Equal(sealed.Scope) ||
		sealed.OwnerID != target.TargetDatabaseID || sealed.ProviderResourceID != target.ProviderResourceID || !sealed.ProviderCreatedAt.Equal(target.ProviderCreatedAt) {
		return ProjectEnvironmentClonePostgresTargetSQLPins{}, false, ErrConflict
	}
	r, err := readClonePostgresTargetSQLPinsTx(ctx, tx, target, inventory)
	created := errors.Is(err, ErrNotFound)
	if created {
		raw, marshalErr := json.Marshal(scope)
		if marshalErr != nil {
			return r, false, ErrConflict
		}
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresTargetSQLPins(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresTargetSQLPinsParams{
			OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(id), AccountID: mustPgUUID(scope.AccountID), ProjectID: mustPgUUID(scope.ProjectID), TargetDatabaseID: mustPgUUID(target.TargetDatabaseID),
			TargetProviderResourceID: target.ProviderResourceID, TargetProviderCreatedAt: pgtype.Timestamptz{Time: target.ProviderCreatedAt, Valid: true}, Scope: raw, InventoryFingerprint: fingerprint, TargetFingerprint: sealed.Fingerprint,
			KeyID: sealed.KeyID, Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return r, false, cloneCopyReaderMutationError(err)
		}
		r, err = readClonePostgresTargetSQLPinsTx(ctx, tx, target, inventory)
	}
	if err != nil {
		return r, false, err
	}
	if r.Sealed.Fingerprint != sealed.Fingerprint || r.Sealed.KeyID != sealed.KeyID || r.Sealed.CiphertextSHA256 != sealed.CiphertextSHA256 || !bytes.Equal(r.Sealed.Ciphertext, sealed.Ciphertext) {
		return r, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return r, false, err
	}
	return r, created, mapErr(tx.Commit(ctx))
}
