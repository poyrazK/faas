package state

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func clonePostgresBindingContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneOperation, ProjectEnvironmentClonePostgresBindingTarget, error) {
	op, views, err := cloneObjectCredentialContextTx(ctx, tx, lease)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, err
	}
	appID, source, err := capturedClonePostgresBinding(views, sourceID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, err
	}
	q := new(sqlc.Queries)
	if _, err := q.LockProjectEnvironmentCloneDatabaseAccount(ctx, tx, mustPgUUID(op.AccountID)); err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, mapErr(err)
	}
	if _, err := q.ObjectBucketLockApp(ctx, tx, sqlc.ObjectBucketLockAppParams{ID: mustPgUUID(appID), AccountID: mustPgUUID(op.AccountID)}); err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, mapErr(err)
	}
	databaseSource, resource, point, err := capturedCloneDatabaseReservation(op, views, source.DatabaseID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, err
	}
	database, err := readCloneDatabaseReservationTx(ctx, tx, op, source.DatabaseID)
	if err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, mapErr(err)
	}
	if err := validateCloneDatabaseReservation(op, databaseSource, resource, point, database); err != nil {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, err
	}
	if resource.Status != "ready" || resource.TargetID != pgUUIDString(database.ID) || database.State != "ready" {
		return op, ProjectEnvironmentClonePostgresBindingTarget{}, ErrConflict
	}
	want := ProjectEnvironmentClonePostgresBindingTarget{OperationID: op.ID, SourceBindingID: source.ID, AccountID: op.AccountID, AppID: appID, DatabaseID: pgUUIDString(database.ID),
		Scope: op.TargetEnvironment, EnvironmentKey: source.EnvironmentKey, Access: source.Access, BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
		DatabaseProviderResourceID: database.ProviderResourceID.String, SourceDatabaseVersion: resource.SourceVersion, CapturePoint: resource.CapturePoint, CredentialGeneration: 1, State: "provisioning"}
	if err := q.LockProjectEnvironmentCloneSecretTarget(ctx, tx, sqlc.LockProjectEnvironmentCloneSecretTargetParams{AppID: mustPgUUID(appID), Scope: want.Scope, Key: want.EnvironmentKey}); err != nil {
		return op, want, mapErr(err)
	}
	return op, want, nil
}

func clonePostgresBindingLedgerTx(ctx context.Context, tx pgx.Tx, want ProjectEnvironmentClonePostgresBindingTarget) (ProjectEnvironmentClonePostgresBindingTarget, *ProjectEnvironmentClonePostgresBindingPreparation, error) {
	q := new(sqlc.Queries)
	ledger, err := q.ReadProjectEnvironmentClonePostgresBindingLedger(ctx, tx, sqlc.ReadProjectEnvironmentClonePostgresBindingLedgerParams{OperationID: mustPgUUID(want.OperationID), SourceBindingID: mustPgUUID(want.SourceBindingID)})
	if err != nil {
		return want, nil, mapErr(err)
	}
	want.ID = pgUUIDString(ledger.TargetBindingID)
	hash, err := clonePostgresBindingReservationHash(want)
	if err != nil || hash != ledger.ReservationHash || want.ID == want.SourceBindingID {
		return want, nil, ErrConflict
	}
	actual, err := q.LockProjectEnvironmentClonePostgresBinding(ctx, tx, sqlc.LockProjectEnvironmentClonePostgresBindingParams{ID: ledger.TargetBindingID, AccountID: mustPgUUID(want.AccountID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return want, nil, ErrConflict
	}
	if err != nil {
		return want, nil, mapErr(err)
	}
	if pgUUIDString(actual.DatabaseID) != want.DatabaseID || pgUUIDString(actual.AppID) != want.AppID || actual.Scope != want.Scope || actual.EnvironmentKey != want.EnvironmentKey ||
		actual.Access != want.Access || actual.CredentialGeneration != 1 || actual.DeletedAt.Valid || actual.LeaseToken.Valid || actual.RotationPreviousGeneration.Valid || actual.RotationWakeID.Valid || actual.RotationCleanupReady {
		return want, nil, ErrConflict
	}
	secrets, err := q.ReadProjectEnvironmentClonePostgresBindingSecrets(ctx, tx, ledger.TargetBindingID)
	if err != nil {
		return want, nil, mapErr(err)
	}
	if !ledger.PreparationHash.Valid {
		if actual.State != "provisioning" || actual.ProviderIdentityID.Valid || actual.CredentialRef.Valid || len(secrets) != 0 || len(ledger.Preparation) != 0 {
			return want, nil, ErrConflict
		}
		return want, nil, nil
	}
	if actual.State != "ready" || !actual.ProviderIdentityID.Valid || actual.ProviderIdentityID.String == "" || actual.CredentialRef.String != clonePostgresCredentialRef(want.ID, 1) || len(secrets) != 1 {
		return want, nil, ErrConflict
	}
	want.State, want.ProviderIdentityID, want.CredentialRef = actual.State, actual.ProviderIdentityID.String, actual.CredentialRef.String
	r := secrets[0]
	secret := AppSecret{AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Scope: r.Scope, Key: r.Key, Ciphertext: r.Ciphertext, Kid: r.Kid.String, ValueHash: r.ValueHash.String,
		SecretClass: r.SecretClass, SecretVersion: r.SecretVersion.Int64, ManagedPostgresBindingID: pgUUIDString(r.ManagedPostgresBindingID), ManagedCredentialRef: r.ManagedCredentialRef.String,
		ManagedCredentialGeneration: r.ManagedCredentialGeneration.Int64, ManagedObjectStorageCredentialID: pgUUIDString(r.ManagedObjectStorageCredentialID)}
	if err := validateClonePostgresPreparationSecret(want, secret); err != nil {
		return want, nil, err
	}
	prepared, _, err := normalizeClonePostgresPreparation(ProjectEnvironmentClonePostgresBindingPreparation{Binding: want, Secret: secret})
	if err != nil || prepared.Hash != ledger.PreparationHash.String {
		return want, nil, ErrConflict
	}
	var receipt ProjectEnvironmentClonePostgresBindingPreparation
	if json.Unmarshal(ledger.Preparation, &receipt) != nil {
		return want, nil, ErrConflict
	}
	receipt, _, err = normalizeClonePostgresPreparation(receipt)
	if err != nil || receipt.Hash != prepared.Hash {
		return want, nil, ErrConflict
	}
	return want, &prepared, nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresBinding(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresBindingTarget, bool, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresBindingTarget{}, false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresBindingTarget{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, want, err := clonePostgresBindingContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return want, false, err
	}
	if op.Status != CloneOperationCopying {
		return want, false, ErrConflict
	}
	existing, _, err := clonePostgresBindingLedgerTx(ctx, tx, want)
	if err == nil {
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return want, false, err
		}
		return existing, false, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return want, false, err
	}
	q := new(sqlc.Queries)
	exists, err := q.ProjectEnvironmentCloneSecretTargetExists(ctx, tx, sqlc.ProjectEnvironmentCloneSecretTargetExistsParams{AppID: mustPgUUID(want.AppID), Scope: want.Scope, Key: want.EnvironmentKey})
	if err != nil {
		return want, false, mapErr(err)
	}
	if exists {
		return want, false, ErrConflict
	}
	want.ID = uuid.NewString()
	if _, err := q.InsertProjectEnvironmentClonePostgresBinding(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresBindingParams{ID: mustPgUUID(want.ID), AccountID: mustPgUUID(want.AccountID), DatabaseID: mustPgUUID(want.DatabaseID),
		AppID: mustPgUUID(want.AppID), Scope: want.Scope, EnvironmentKey: want.EnvironmentKey, Access: want.Access}); err != nil {
		return want, false, mapErr(err)
	}
	hash, err := clonePostgresBindingReservationHash(want)
	if err != nil {
		return want, false, err
	}
	if err := q.InsertProjectEnvironmentClonePostgresBindingLedger(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresBindingLedgerParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(sourceID), TargetBindingID: mustPgUUID(want.ID), ReservationHash: hash}); err != nil {
		return want, false, mapErr(err)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return want, false, err
	}
	return want, true, tx.Commit(ctx)
}

func (s *PgStore) ProjectEnvironmentClonePostgresBindingForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresBindingTarget, *ProjectEnvironmentClonePostgresBindingPreparation, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresBindingTarget{}, nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresBindingTarget{}, nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, want, err := clonePostgresBindingContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return want, nil, err
	}
	target, prepared, err := clonePostgresBindingLedgerTx(ctx, tx, want)
	if err != nil {
		return target, nil, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return target, nil, err
	}
	return target, prepared, tx.Commit(ctx)
}

func (s *PgStore) PrepareProjectEnvironmentClonePostgresBinding(ctx context.Context, lease ProjectEnvironmentCloneLease, request ProjectEnvironmentClonePostgresBindingRequest) (ProjectEnvironmentClonePostgresBindingPreparation, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(request.SourceBindingID) || !validCloneCredentialSourceID(request.TargetBindingID) || request.MaxSecretsPerApp < 1 {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, want, err := clonePostgresBindingContextTx(ctx, tx, lease, request.SourceBindingID)
	if err != nil {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	if op.Status != CloneOperationCopying {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, ErrConflict
	}
	want, existing, err := clonePostgresBindingLedgerTx(ctx, tx, want)
	if err != nil {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	if want.ID != request.TargetBindingID || request.CredentialRef != clonePostgresCredentialRef(want.ID, 1) || request.ProviderIdentityID == "" || len(request.ProviderIdentityID) > 1024 || strings.ContainsAny(request.ProviderIdentityID, "\x00\r\n") {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, ErrConflict
	}
	if err := validateClonePostgresPreparationSecret(want, request.Secret); err != nil {
		return ProjectEnvironmentClonePostgresBindingPreparation{}, err
	}
	want.State, want.ProviderIdentityID, want.CredentialRef = "ready", request.ProviderIdentityID, request.CredentialRef
	prepared, raw, err := normalizeClonePostgresPreparation(ProjectEnvironmentClonePostgresBindingPreparation{Binding: want, Secret: request.Secret})
	if err != nil {
		return prepared, err
	}
	if existing != nil {
		if existing.Hash != prepared.Hash {
			return prepared, ErrConflict
		}
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return prepared, err
		}
		return *existing, tx.Commit(ctx)
	}
	q := new(sqlc.Queries)
	count, err := q.ObjectS3BindingSecretCount(ctx, tx, sqlc.ObjectS3BindingSecretCountParams{AccountID: mustPgUUID(want.AccountID), AppID: mustPgUUID(want.AppID)})
	if err != nil {
		return prepared, mapErr(err)
	}
	if count >= int64(request.MaxSecretsPerApp) {
		return prepared, ErrQuotaExceeded
	}
	secret := prepared.Secret
	if err := q.InsertProjectEnvironmentClonePostgresSecret(ctx, tx, sqlc.InsertProjectEnvironmentClonePostgresSecretParams{AccountID: mustPgUUID(secret.AccountID), AppID: mustPgUUID(secret.AppID), Scope: secret.Scope, Key: secret.Key,
		Ciphertext: secret.Ciphertext, Kid: pgtype.Text{String: secret.Kid, Valid: true}, ValueHash: pgtype.Text{String: secret.ValueHash, Valid: secret.ValueHash != ""}, ManagedPostgresBindingID: mustPgUUID(want.ID), ManagedCredentialRef: pgtype.Text{String: request.CredentialRef, Valid: true}}); err != nil {
		return prepared, mapErr(err)
	}
	changed, err := q.FinishProjectEnvironmentClonePostgresBinding(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresBindingParams{ID: mustPgUUID(want.ID), AccountID: mustPgUUID(want.AccountID), ProviderIdentityID: pgtype.Text{String: request.ProviderIdentityID, Valid: true}, CredentialRef: pgtype.Text{String: request.CredentialRef, Valid: true}})
	if err != nil {
		return prepared, mapErr(err)
	}
	if changed != 1 {
		return prepared, ErrConflict
	}
	changed, err = q.FinishProjectEnvironmentClonePostgresBindingLedger(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresBindingLedgerParams{OperationID: mustPgUUID(op.ID), SourceBindingID: mustPgUUID(request.SourceBindingID), PreparationHash: pgtype.Text{String: prepared.Hash, Valid: true}, Preparation: raw})
	if err != nil {
		return prepared, mapErr(err)
	}
	if changed != 1 {
		return prepared, ErrConflict
	}
	_, verified, err := clonePostgresBindingLedgerTx(ctx, tx, want)
	if err != nil || verified == nil {
		if err == nil {
			err = ErrConflict
		}
		return prepared, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return prepared, err
	}
	return *verified, tx.Commit(ctx)
}
