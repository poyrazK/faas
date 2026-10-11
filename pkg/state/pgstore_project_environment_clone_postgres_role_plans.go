package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresRolePlanContextTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresInventory, ProjectEnvironmentClonePostgresTargetSQLPins, error) {
	op, err := clonePostgresSnapshotLeaseTx(ctx, tx, l)
	if err != nil {
		return ProjectEnvironmentClonePostgresInventory{}, ProjectEnvironmentClonePostgresTargetSQLPins{}, err
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentClonePostgresInventory{}, ProjectEnvironmentClonePostgresTargetSQLPins{}, ErrConflict
	}
	target, inventory, err := clonePostgresTargetSQLPinsContextTx(ctx, tx, l, id)
	if err != nil {
		return inventory, ProjectEnvironmentClonePostgresTargetSQLPins{}, err
	}
	if target.State != "prepared" {
		return inventory, ProjectEnvironmentClonePostgresTargetSQLPins{}, ErrConflict
	}
	pins, err := readClonePostgresTargetSQLPinsTx(ctx, tx, target, inventory)
	return inventory, pins, err
}

func readClonePostgresRolePlanTx(ctx context.Context, tx pgx.Tx, inventory ProjectEnvironmentClonePostgresInventory, pins ProjectEnvironmentClonePostgresTargetSQLPins) (ProjectEnvironmentClonePostgresRolePlan, error) {
	scope := inventory.Sealed.Scope
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresRolePlan(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresRolePlanParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresRolePlan{}, mapErr(err)
	}
	stored, err := decodeCloneArchiveScope(r.Scope)
	sealed := copyroles.Sealed{Scope: stored, InventoryFingerprint: r.InventoryFingerprint, TargetFingerprint: r.TargetFingerprint, KeyID: r.KeyID, CiphertextSHA256: r.CiphertextSha256, Ciphertext: bytes.Clone(r.Ciphertext)}
	if err != nil || !stored.Equal(scope) || sealed.ValidateMetadata() != nil || r.InventoryFingerprint != inventory.Sealed.Fingerprint || r.InventoryCiphertextSha256 != inventory.Sealed.CiphertextSHA256 ||
		r.TargetFingerprint != pins.Sealed.Fingerprint || r.TargetPinsCiphertextSha256 != pins.Sealed.CiphertextSHA256 || pgUUIDString(r.TargetDatabaseID) != pins.Sealed.OwnerID ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || !r.CapturedAt.Valid || r.CapturedAt.Time.Before(pins.CapturedAt) || r.CapturedAt.Time.Before(inventory.CapturedAt) {
		return ProjectEnvironmentClonePostgresRolePlan{}, ErrConflict
	}
	return ProjectEnvironmentClonePostgresRolePlan{Sealed: sealed, TargetDatabaseID: pgUUIDString(r.TargetDatabaseID), InventoryCiphertextSHA256: r.InventoryCiphertextSha256, TargetPinsCiphertextSHA256: r.TargetPinsCiphertextSha256, CapturedAt: r.CapturedAt.Time}, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresRolePlanForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresRolePlan, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ProjectEnvironmentClonePostgresRolePlan{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresRolePlan{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	inventory, pins, err := clonePostgresRolePlanContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresRolePlan{}, err
	}
	r, err := readClonePostgresRolePlanTx(ctx, tx, inventory, pins)
	if err != nil {
		return r, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

// The first committed ciphertext and recipient win. Read the retained record
// before inspecting target SQL: equivalent re-encryption is not replacement.
func (s *PgStore) RecordProjectEnvironmentClonePostgresRolePlan(ctx context.Context, l ProjectEnvironmentCloneLease, id string, sealed copyroles.Sealed) (ProjectEnvironmentClonePostgresRolePlan, bool, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ProjectEnvironmentClonePostgresRolePlan{}, false, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, pgerrors.ErrQuotaExceeded) {
			return ProjectEnvironmentClonePostgresRolePlan{}, false, ErrQuotaExceeded
		}
		return ProjectEnvironmentClonePostgresRolePlan{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresRolePlan{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	inventory, pins, err := clonePostgresRolePlanContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresRolePlan{}, false, err
	}
	if !sealed.Scope.Equal(inventory.Sealed.Scope) || sealed.InventoryFingerprint != inventory.Sealed.Fingerprint || sealed.TargetFingerprint != pins.Sealed.Fingerprint {
		return ProjectEnvironmentClonePostgresRolePlan{}, false, ErrConflict
	}
	r, err := readClonePostgresRolePlanTx(ctx, tx, inventory, pins)
	created := errors.Is(err, ErrNotFound)
	if created {
		raw, marshalErr := json.Marshal(sealed.Scope)
		if marshalErr != nil {
			return r, false, ErrConflict
		}
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresRolePlan(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresRolePlanParams{
			OperationID: mustPgUUID(sealed.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), AccountID: mustPgUUID(sealed.Scope.AccountID), ProjectID: mustPgUUID(sealed.Scope.ProjectID), TargetDatabaseID: mustPgUUID(pins.Sealed.OwnerID),
			Scope: raw, InventoryFingerprint: sealed.InventoryFingerprint, InventoryCiphertextSha256: inventory.Sealed.CiphertextSHA256, TargetFingerprint: sealed.TargetFingerprint, TargetPinsCiphertextSha256: pins.Sealed.CiphertextSHA256,
			KeyID: sealed.KeyID, Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return r, false, cloneCopyReaderMutationError(err)
		}
		r, err = readClonePostgresRolePlanTx(ctx, tx, inventory, pins)
	}
	if err != nil {
		return r, false, err
	}
	if r.Sealed.KeyID != sealed.KeyID || r.Sealed.CiphertextSHA256 != sealed.CiphertextSHA256 || !bytes.Equal(r.Sealed.Ciphertext, sealed.Ciphertext) {
		return r, false, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return r, false, err
	}
	return r, created, mapErr(tx.Commit(ctx))
}
