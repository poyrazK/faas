package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresDatabaseSQLPinsContextTx(ctx context.Context, tx pgx.Tx, l ProjectEnvironmentCloneLease, id string, oid uint32) (ProjectEnvironmentClonePostgresDatabasePlan, ProjectEnvironmentClonePostgresTargetSQLPins, ProjectEnvironmentClonePostgresArchive, error) {
	var zero ProjectEnvironmentClonePostgresDatabasePlan
	inventory, pins, err := clonePostgresRolePlanContextTx(ctx, tx, l, id)
	if err != nil {
		return zero, pins, ProjectEnvironmentClonePostgresArchive{}, err
	}
	role, err := readClonePostgresRolePlanTx(ctx, tx, inventory, pins)
	if err != nil {
		return zero, pins, ProjectEnvironmentClonePostgresArchive{}, err
	}
	plan, err := readClonePostgresDatabasePlanTx(ctx, tx, role)
	if err != nil {
		return plan, pins, ProjectEnvironmentClonePostgresArchive{}, err
	}
	archive, err := readClonePostgresArchiveTx(ctx, tx, plan.Sealed.Scope, plan.Sealed.InventoryFingerprint, oid)
	return plan, pins, archive, err
}

func readClonePostgresDatabaseSQLPinsTx(ctx context.Context, tx pgx.Tx, plan ProjectEnvironmentClonePostgresDatabasePlan, pins ProjectEnvironmentClonePostgresTargetSQLPins, a ProjectEnvironmentClonePostgresArchive) (ProjectEnvironmentClonePostgresDatabaseSQLPins, error) {
	scope := plan.Sealed.Scope
	r, err := new(sqlc.Queries).ReadProjectEnvironmentClonePostgresDatabaseSQLPins(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresDatabaseSQLPinsParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(scope.SourceDatabaseID), DatabaseOid: int64(a.DatabaseOID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, mapErr(err)
	}
	stored, err := decodeCloneArchiveScope(r.Scope)
	sealed := copydatabases.SealedPreparation{Scope: stored, OwnerID: pgUUIDString(r.TargetDatabaseID), ProviderResourceID: r.TargetProviderResourceID, ProviderCreatedAt: r.TargetProviderCreatedAt.Time, Fingerprint: r.TargetFingerprint, KeyID: r.KeyID, CiphertextSHA256: r.CiphertextSha256, Ciphertext: bytes.Clone(r.Ciphertext)}
	if err != nil || sealed.ValidateMetadata() != nil || !stored.Equal(scope) || sealed.OwnerID != plan.TargetDatabaseID || sealed.ProviderResourceID != pins.Sealed.ProviderResourceID || !sealed.ProviderCreatedAt.Equal(pins.Sealed.ProviderCreatedAt) ||
		pgUUIDString(r.AccountID) != scope.AccountID || pgUUIDString(r.ProjectID) != scope.ProjectID || r.InventoryFingerprint != plan.Sealed.InventoryFingerprint || r.DatabasePlanCiphertextSha256 != plan.Sealed.CiphertextSHA256 || pgUUIDString(r.ArchiveOwnerID) != a.OwnerID || r.ArchiveReservationSha256 != a.ReservationFingerprint() ||
		!r.CapturedAt.Valid || !r.TargetProviderCreatedAt.Valid || r.CapturedAt.Time.Before(plan.CapturedAt) || r.CapturedAt.Time.Before(a.CreatedAt) {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, ErrConflict
	}
	return ProjectEnvironmentClonePostgresDatabaseSQLPins{Sealed: sealed, DatabasePlanCiphertextSHA256: r.DatabasePlanCiphertextSha256, ArchiveReservationSHA256: r.ArchiveReservationSha256, ArchiveOwnerID: a.OwnerID, CapturedAt: r.CapturedAt.Time}, nil
}

func (s *PgStore) ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32) (ProjectEnvironmentClonePostgresDatabaseSQLPins, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || oid == 0 {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	plan, pins, a, err := clonePostgresDatabaseSQLPinsContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, err
	}
	r, err := readClonePostgresDatabaseSQLPinsTx(ctx, tx, plan, pins, a)
	if err != nil {
		return r, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, l.Operation, &l); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

// Both first-capture prerequisite hashes come from the worker's original inputs.
// Equivalent re-encryption or a changed reservation cannot replace occupied pins.
func (s *PgStore) RecordProjectEnvironmentClonePostgresDatabaseSQLPins(ctx context.Context, l ProjectEnvironmentCloneLease, id string, oid uint32, planSHA, archiveSHA string, sealed copydatabases.SealedPreparation) (ProjectEnvironmentClonePostgresDatabaseSQLPins, bool, error) {
	if !validCloneLeaseIdentity(l) || !validCloneCredentialSourceID(id) || oid == 0 || !validCloneObjectSHA256(planSHA) || !validCloneObjectSHA256(archiveSHA) {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, ErrInvalidArgument
	}
	if err := sealed.ValidateMetadata(); err != nil {
		if errors.Is(err, pgerrors.ErrQuotaExceeded) {
			return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, ErrQuotaExceeded
		}
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	plan, pins, a, err := clonePostgresDatabaseSQLPinsContextTx(ctx, tx, l, id, oid)
	if err != nil {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, err
	}
	scope := plan.Sealed.Scope
	if planSHA != plan.Sealed.CiphertextSHA256 || archiveSHA != a.ReservationFingerprint() || !sealed.Scope.Equal(scope) || sealed.OwnerID != plan.TargetDatabaseID || sealed.ProviderResourceID != pins.Sealed.ProviderResourceID || !sealed.ProviderCreatedAt.Equal(pins.Sealed.ProviderCreatedAt) {
		return ProjectEnvironmentClonePostgresDatabaseSQLPins{}, false, ErrConflict
	}
	r, err := readClonePostgresDatabaseSQLPinsTx(ctx, tx, plan, pins, a)
	created := errors.Is(err, ErrNotFound)
	if created {
		raw, _ := json.Marshal(scope)
		_, err = new(sqlc.Queries).InsertProjectEnvironmentClonePostgresDatabaseSQLPins(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresDatabaseSQLPinsParams{OperationID: mustPgUUID(scope.OperationID), SourceDatabaseID: mustPgUUID(id), DatabaseOid: int64(oid), AccountID: mustPgUUID(scope.AccountID), ProjectID: mustPgUUID(scope.ProjectID), TargetDatabaseID: mustPgUUID(plan.TargetDatabaseID), ArchiveOwnerID: mustPgUUID(a.OwnerID), ArchiveReservationSha256: archiveSHA, DatabasePlanCiphertextSha256: planSHA, TargetProviderResourceID: sealed.ProviderResourceID, TargetProviderCreatedAt: pgtype.Timestamptz{Time: sealed.ProviderCreatedAt, Valid: true}, Scope: raw, InventoryFingerprint: plan.Sealed.InventoryFingerprint, TargetFingerprint: sealed.Fingerprint, KeyID: sealed.KeyID, Ciphertext: bytes.Clone(sealed.Ciphertext), CiphertextSha256: sealed.CiphertextSHA256, ExpectedRevision: l.Operation.Revision, WorkerToken: l.Token})
		if err != nil {
			return r, false, cloneCopyReaderMutationError(err)
		}
		r, err = readClonePostgresDatabaseSQLPinsTx(ctx, tx, plan, pins, a)
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
