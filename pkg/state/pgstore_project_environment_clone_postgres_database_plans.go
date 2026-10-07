package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresDatabasePlanContextTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresRolePlan, error) {
	inventory, pins, err := clonePostgresRolePlanContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresRolePlan{}, err
	}
	return readClonePostgresRolePlanTx(ctx, tx, inventory, pins)
}

func readClonePostgresDatabasePlanTx(ctx context.Context, tx pgx.Tx, role ProjectEnvironmentClonePostgresRolePlan) (ProjectEnvironmentClonePostgresDatabasePlan, error) {
	scope := role.Sealed.Scope
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresDatabasePlan(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresDatabasePlanParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, mapErr(err)
	}
	stored, err := decodeCloneArchiveScope(r.Scope)
	sealed := copydatabases.Sealed{Scope: stored, InventoryFingerprint: r.InventoryFingerprint, TargetFingerprint: r.TargetFingerprint, KeyID: r.KeyID, CiphertextSHA256: r.CiphertextSha256, Ciphertext: bytes.Clone(r.Ciphertext)}
	if err != nil || !stored.Equal(scope) || sealed.ValidateMetadata() != nil || r.InventoryFingerprint != role.Sealed.InventoryFingerprint || r.InventoryCiphertextSha256 != role.InventoryCiphertextSHA256 ||
		r.TargetFingerprint != role.Sealed.TargetFingerprint || r.TargetPinsCiphertextSha256 != role.TargetPinsCiphertextSHA256 || r.RolePlanCiphertextSha256 != role.Sealed.CiphertextSHA256 || pgUUIDString(r.TargetDatabaseID) != role.TargetDatabaseID ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || !r.CapturedAt.Valid || r.CapturedAt.Time.Before(role.CapturedAt) {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, ErrConflict
	}
	return ProjectEnvironmentClonePostgresDatabasePlan{Sealed: sealed, TargetDatabaseID: pgUUIDString(r.TargetDatabaseID), InventoryCiphertextSHA256: r.InventoryCiphertextSha256, TargetPinsCiphertextSHA256: r.TargetPinsCiphertextSha256, RolePlanCiphertextSHA256: r.RolePlanCiphertextSha256, CapturedAt: r.CapturedAt.Time}, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresDatabasePlanForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string) (ProjectEnvironmentClonePostgresDatabasePlan, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	role, err := clonePostgresDatabasePlanContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, err
	}
	r, err := readClonePostgresDatabasePlanTx(ctx, tx, role)
	if err != nil {
		return r, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

// The worker supplies the exact role-plan ciphertext it used. A changed parent
// cannot be adopted during capture, even when its logical fingerprints match.
func (s *PgStore) RecordProjectEnvironmentClonePostgresDatabasePlan(ctx context.Context, l ProjectEnvironmentCloneLease, id, roleCipherSHA256 string, sealed copydatabases.Sealed) (ProjectEnvironmentClonePostgresDatabasePlan, bool, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || !validCloneObjectSHA256(roleCipherSHA256) {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, false, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, pgerrors.ErrQuotaExceeded) {
			return ProjectEnvironmentClonePostgresDatabasePlan{}, false, ErrQuotaExceeded
		}
		return ProjectEnvironmentClonePostgresDatabasePlan{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	role, err := clonePostgresDatabasePlanContextTx(ctx, tx, l, id)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, false, err
	}
	if !sealed.Scope.Equal(role.Sealed.Scope) || sealed.InventoryFingerprint != role.Sealed.InventoryFingerprint || sealed.TargetFingerprint != role.Sealed.TargetFingerprint || roleCipherSHA256 != role.Sealed.CiphertextSHA256 {
		return ProjectEnvironmentClonePostgresDatabasePlan{}, false, ErrConflict
	}
	r, err := readClonePostgresDatabasePlanTx(ctx, tx, role)
	created := errors.Is(err, ErrNotFound)
	if created {
		raw, marshalErr := json.Marshal(sealed.Scope)
		if marshalErr != nil {
			return r, false, ErrConflict
		}
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresDatabasePlan(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresDatabasePlanParams{
			OperationID: mustPgUUID(sealed.Scope.OperationID), SourceDatabaseID: mustPgUUID(id), AccountID: mustPgUUID(sealed.Scope.AccountID), ProjectID: mustPgUUID(sealed.Scope.ProjectID), TargetDatabaseID: mustPgUUID(role.TargetDatabaseID),
			Scope: raw, InventoryFingerprint: sealed.InventoryFingerprint, InventoryCiphertextSha256: role.InventoryCiphertextSHA256, TargetFingerprint: sealed.TargetFingerprint, TargetPinsCiphertextSha256: role.TargetPinsCiphertextSHA256, RolePlanCiphertextSha256: roleCipherSHA256,
			KeyID: sealed.KeyID, Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return r, false, cloneCopyReaderMutationError(err)
		}
		r, err = readClonePostgresDatabasePlanTx(ctx, tx, role)
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
