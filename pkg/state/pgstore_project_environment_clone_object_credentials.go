package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cloneObjectCredentialContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneOperation, []ProjectEnvironmentCloneBindings, error) {
	identity := lease.Operation
	op, err := lockCloneWorkloadOperationTx(ctx, tx, identity.AccountID, identity.ProjectID, identity.ID)
	if err != nil {
		return op, nil, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return op, nil, err
	}
	if op.Status != CloneOperationCopying && op.Status != CloneOperationPublishing {
		return op, nil, ErrConflict
	}

	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return op, nil, err
	}
	views, err := cloneBindingViews(records)
	return op, views, err
}

func validateCloneCredentialBucketTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, views []ProjectEnvironmentCloneBindings, request ProjectEnvironmentCloneObjectCredentialRequest) error {
	q := new(sqlc.Queries)
	want, source, err := capturedCloneBucketReservation(op, views, request.AppID, request.SourceBucketID)
	if err != nil {
		return err
	}
	if _, err := q.ObjectBucketLockApp(ctx, tx, sqlc.ObjectBucketLockAppParams{ID: mustPgUUID(request.AppID), AccountID: mustPgUUID(op.AccountID)}); err != nil {
		return mapErr(err)
	}
	row, err := q.LockProjectEnvironmentCloneCredentialBucket(ctx, tx, sqlc.LockProjectEnvironmentCloneCredentialBucketParams{
		AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(request.AppID), ID: mustPgUUID(request.Target.Credential.BucketID), EnvironmentCloneOperationID: mustPgUUID(op.ID),
	})
	if err != nil {
		return mapErr(err)
	}
	bucket := objectBucketFromSQL(row)
	if err := validateCloneBucketReservation(want, bucket, source); err != nil {
		return err
	}
	manifest, err := projectEnvironmentCloneObjectManifestDB(ctx, tx, op.AccountID, op.ProjectID, op.ID, source.ID)
	if err != nil {
		return err
	}
	return validateCloneObjectCredentialCopy(op, source.ID, bucket.ID, manifest)
}

func cloneObjectCredentialPreparationDB(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, sourceCredentialID string) (ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	q := new(sqlc.Queries)
	row, err := q.ReadProjectEnvironmentCloneObjectCredentialPreparation(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectCredentialPreparationParams{
		AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID), ID: mustPgUUID(op.ID), SourceCredentialID: mustPgUUID(sourceCredentialID),
	})
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, mapErr(err)
	}
	var prepared ProjectEnvironmentCloneObjectCredentialPreparation
	if err := json.Unmarshal(row.Preparation, &prepared); err != nil {
		return prepared, ErrConflict
	}
	prepared, _, err = normalizeCloneObjectCredentialPreparation(prepared)
	if err != nil || prepared.Hash != row.PreparationHash || prepared.OperationID != op.ID || prepared.SourceCredentialID != sourceCredentialID || prepared.Credential.AccountID != op.AccountID {
		return prepared, ErrConflict
	}
	credential, err := q.ObjectS3CredentialGet(ctx, tx, sqlc.ObjectS3CredentialGetParams{ID: mustPgUUID(prepared.Credential.ID), AccountID: mustPgUUID(op.AccountID), BucketID: mustPgUUID(prepared.Credential.BucketID)})
	if err != nil {
		return prepared, mapErr(err)
	}
	actual := prepared
	actual.Credential, actual.Secrets = objectS3CredentialFromSQL(credential), nil
	secrets, err := q.ReadProjectEnvironmentCloneObjectCredentialSecrets(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectCredentialSecretsParams{
		AccountID: mustPgUUID(op.AccountID), AppID: mustPgUUID(prepared.AppID), Scope: op.TargetEnvironment, ManagedObjectStorageCredentialID: mustPgUUID(prepared.Credential.ID),
	})
	if err != nil {
		return prepared, mapErr(err)
	}
	for _, secret := range secrets {
		actual.Secrets = append(actual.Secrets, AppSecret{AccountID: secret.AccountID, AppID: secret.AppID, Scope: secret.Scope, Key: secret.Key, Ciphertext: secret.Ciphertext,
			Kid: secret.Kid, ValueHash: secret.ValueHash, SecretClass: secret.SecretClass, SecretVersion: secret.SecretVersion.Int64, ManagedObjectStorageCredentialID: secret.ManagedObjectStorageCredentialID})
	}
	actual, _, err = normalizeCloneObjectCredentialPreparation(actual)
	if err != nil || actual.Hash != prepared.Hash {
		return prepared, ErrConflict
	}
	return actual, nil
}

func (s *PgStore) PrepareProjectEnvironmentCloneObjectCredential(ctx context.Context, lease ProjectEnvironmentCloneLease, request ProjectEnvironmentCloneObjectCredentialRequest) (ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	if !validCloneLeaseIdentity(lease) {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, views, err := cloneObjectCredentialContextTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	if op.Status != CloneOperationCopying {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrConflict
	}
	if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	if err := validateCloneCredentialBucketTx(ctx, tx, op, views, request); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	prepared, raw, err := newCloneObjectCredentialPreparation(op, request)
	if err != nil {
		return prepared, err
	}
	existing, err := cloneObjectCredentialPreparationDB(ctx, tx, op, request.SourceCredentialID)
	if err == nil {
		if existing.Hash != prepared.Hash {
			return prepared, ErrConflict
		}
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return prepared, err
	}
	if request.Target.Credential.ManagedAppID == "" {
		_, err = insertObjectS3CredentialTx(ctx, tx, request.Target.Credential, request.Target.MaxCredentialsPerBucket)
	} else {
		_, err = insertObjectS3ComputeBindingTx(ctx, tx, request.Target, false)
	}
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	if err := new(sqlc.Queries).InsertProjectEnvironmentCloneObjectCredentialPreparation(ctx, tx, sqlc.InsertProjectEnvironmentCloneObjectCredentialPreparationParams{
		OperationID: mustPgUUID(op.ID), SourceCredentialID: mustPgUUID(request.SourceCredentialID), TargetCredentialID: mustPgUUID(request.Target.Credential.ID), PreparationHash: prepared.Hash, Preparation: raw,
	}); err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, mapErr(err)
	}
	// Read through the receipt and actual rows before committing, so unexpected
	// normalization or a missing envelope cannot produce a durable false proof.
	prepared, err = cloneObjectCredentialPreparationDB(ctx, tx, op, request.SourceCredentialID)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	return prepared, tx.Commit(ctx)
}

func (s *PgStore) ProjectEnvironmentCloneObjectCredentialForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceCredentialID string) (ProjectEnvironmentCloneObjectCredentialPreparation, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceCredentialID) {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, views, err := cloneObjectCredentialContextTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentCloneObjectCredentialPreparation{}, err
	}
	prepared, err := cloneObjectCredentialPreparationDB(ctx, tx, op, sourceCredentialID)
	if err != nil {
		return prepared, err
	}
	request := cloneObjectCredentialReplayRequest(prepared)
	if err := validateCloneObjectCredentialRequest(op, views, request); err != nil {
		return prepared, err
	}
	if err := validateCloneCredentialBucketTx(ctx, tx, op, views, request); err != nil {
		return prepared, err
	}
	return prepared, tx.Commit(ctx)
}
