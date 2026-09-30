package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgUUIDStringNullable(value pgtype.UUID) string {
	if !value.Valid {
		return ""
	}
	return pgUUIDString(value)
}

var _ ObjectS3CredentialStore = (*PgStore)(nil)
var _ ObjectS3CredentialRekeyStore = (*PgStore)(nil)
var _ ObjectS3CredentialBindingStore = (*PgStore)(nil)

func (s *PgStore) CreateObjectS3Credential(ctx context.Context, credential ObjectS3Credential, maxPerBucket int) (ObjectS3Credential, error) {
	if !validObjectS3Credential(credential) || maxPerBucket < 1 {
		return ObjectS3Credential{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectS3CredentialLockBucket(ctx, tx, sqlc.ObjectS3CredentialLockBucketParams{ID: mustPgUUID(credential.BucketID), AccountID: mustPgUUID(credential.AccountID)}); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	created, err := insertObjectS3CredentialTx(ctx, tx, credential, maxPerBucket)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	return created, tx.Commit(ctx)
}

func insertObjectS3CredentialTx(ctx context.Context, tx pgx.Tx, credential ObjectS3Credential, maxPerBucket int) (ObjectS3Credential, error) {
	q := sqlc.New()
	count, err := q.ObjectS3CredentialCount(ctx, tx, mustPgUUID(credential.BucketID))
	if err != nil {
		return ObjectS3Credential{}, err
	}
	if count >= int64(maxPerBucket) {
		return ObjectS3Credential{}, ErrConflict
	}
	row, err := q.ObjectS3CredentialInsert(ctx, tx, sqlc.ObjectS3CredentialInsertParams{
		ID: mustPgUUID(credential.ID), AccountID: mustPgUUID(credential.AccountID), BucketID: mustPgUUID(credential.BucketID),
		AccessKeyID: credential.AccessKeyID, SecretSealed: credential.SecretSealed, Kid: credential.KID, Label: credential.Label, Permission: credential.Permission,
		Column9: credential.ManagedAppID, Column10: credential.ManagedScope, Column11: credential.ManagedPrefix,
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	return objectS3CredentialFromSQL(row), nil
}

// CreateObjectS3ComputeBinding makes the credential, six runtime secrets,
// freshness stamp and snapshot invalidation visible at one commit boundary.
func (s *PgStore) CreateObjectS3ComputeBinding(ctx context.Context, req ObjectS3ComputeBindingCreateRequest) (ObjectS3Credential, error) {
	if !validObjectS3ComputeBindingCreateRequest(req) {
		return ObjectS3Credential{}, ErrInvalidArgument
	}
	c := req.Credential
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err := q.ObjectS3CredentialLockBucket(ctx, tx, sqlc.ObjectS3CredentialLockBucketParams{ID: mustPgUUID(c.BucketID), AccountID: mustPgUUID(c.AccountID)}); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if _, err := q.ObjectS3BindingLockApp(ctx, tx, sqlc.ObjectS3BindingLockAppParams{AppID: mustPgUUID(c.ManagedAppID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID)}); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	created, err := insertObjectS3ComputeBindingTx(ctx, tx, req, true)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	return created, mapErr(tx.Commit(ctx))
}

func insertObjectS3ComputeBindingTx(ctx context.Context, tx pgx.Tx, req ObjectS3ComputeBindingCreateRequest, invalidateRuntime bool) (ObjectS3Credential, error) {
	q, c := sqlc.New(), req.Credential
	credentialCount, err := q.ObjectS3CredentialCount(ctx, tx, mustPgUUID(c.BucketID))
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	secretCount, err := q.ObjectS3BindingSecretCount(ctx, tx, sqlc.ObjectS3BindingSecretCountParams{AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.ManagedAppID)})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if credentialCount >= int64(req.MaxCredentialsPerBucket) || secretCount+int64(len(req.Secrets)) > int64(req.MaxSecretsPerApp) {
		return ObjectS3Credential{}, ErrConflict
	}
	row, err := q.ObjectS3CredentialInsert(ctx, tx, sqlc.ObjectS3CredentialInsertParams{
		ID: mustPgUUID(c.ID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID),
		AccessKeyID: c.AccessKeyID, SecretSealed: c.SecretSealed, Kid: c.KID, Label: c.Label, Permission: c.Permission,
		Column9: c.ManagedAppID, Column10: c.ManagedScope, Column11: c.ManagedPrefix,
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	for _, secret := range req.Secrets {
		if _, err := q.ObjectS3BindingSecretInsert(ctx, tx, sqlc.ObjectS3BindingSecretInsertParams{
			AccountID: mustPgUUID(secret.AccountID), AppID: mustPgUUID(secret.AppID), Scope: secret.Scope,
			Key: secret.Key, Ciphertext: secret.Ciphertext, Kid: pgtype.Text{String: secret.Kid, Valid: true},
			ValueHash:                        pgtype.Text{String: secret.ValueHash, Valid: true},
			ManagedObjectStorageCredentialID: mustPgUUID(c.ID),
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ObjectS3Credential{}, ErrConflict
			}
			return ObjectS3Credential{}, mapErr(err)
		}
	}
	if invalidateRuntime {
		if err := q.ObjectS3BindingStampRuntime(ctx, tx, mustPgUUID(c.ManagedAppID)); err != nil {
			return ObjectS3Credential{}, mapErr(err)
		}
		if err := q.ObjectS3BindingStaleSnapshots(ctx, tx, mustPgUUID(c.ManagedAppID)); err != nil {
			return ObjectS3Credential{}, mapErr(err)
		}
	}
	return objectS3CredentialFromSQL(row), nil
}

func (s *PgStore) ListObjectS3Credentials(ctx context.Context, accountID, bucketID string) ([]ObjectS3Credential, error) {
	rows, err := sqlc.New().ObjectS3CredentialList(ctx, s.pool, sqlc.ObjectS3CredentialListParams{AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectS3Credential, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectS3CredentialFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) RevokeObjectS3Credential(ctx context.Context, accountID, bucketID, credentialID string) error {
	n, err := sqlc.New().ObjectS3CredentialRevoke(ctx, s.pool, sqlc.ObjectS3CredentialRevokeParams{ID: mustPgUUID(credentialID), AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeObjectS3ComputeBinding atomically revokes the binding and every
// rotation stage, removes its managed secrets, and marks the app's runtime
// configuration and snapshots stale.
func (s *PgStore) RevokeObjectS3ComputeBinding(ctx context.Context, accountID, bucketID, bindingID string) (bool, error) {
	if !validObjectS3ComputeBindingRevokeRequest(accountID, bucketID, bindingID) {
		return false, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	parent, err := q.ObjectS3BindingRevokeLock(ctx, tx, sqlc.ObjectS3BindingRevokeLockParams{
		ID: mustPgUUID(bindingID), AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID),
	})
	if err != nil {
		return false, mapErr(err)
	}
	revoked, err := q.ObjectS3CredentialRevoke(ctx, tx, sqlc.ObjectS3CredentialRevokeParams{
		ID: mustPgUUID(bindingID), AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID),
	})
	if err != nil {
		return false, mapErr(err)
	}
	secrets, err := q.ObjectS3BindingDeleteSecrets(ctx, tx, mustPgUUID(bindingID))
	if err != nil {
		return false, mapErr(err)
	}
	changed := revoked > 0 || secrets > 0
	if changed {
		if err := q.ObjectS3BindingStampRuntime(ctx, tx, parent.ManagedAppID); err != nil {
			return false, mapErr(err)
		}
		if err := q.ObjectS3BindingStaleSnapshots(ctx, tx, parent.ManagedAppID); err != nil {
			return false, mapErr(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, mapErr(err)
	}
	return changed, nil
}

func (s *PgStore) GetObjectS3Credential(ctx context.Context, accountID, bucketID, credentialID string) (ObjectS3Credential, error) {
	row, err := sqlc.New().ObjectS3CredentialGet(ctx, s.pool, sqlc.ObjectS3CredentialGetParams{
		ID: mustPgUUID(credentialID), AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID),
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	return objectS3CredentialFromSQL(row), nil
}

func (s *PgStore) PendingObjectS3CredentialRotation(ctx context.Context, accountID, bucketID, bindingID string) (string, error) {
	wakeID, err := sqlc.New().ObjectS3CredentialRotationPending(ctx, s.pool, sqlc.ObjectS3CredentialRotationPendingParams{
		AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID), RotationParentID: mustPgUUID(bindingID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return wakeID, mapErr(err)
}

func (s *PgStore) StageObjectS3CredentialRotation(ctx context.Context, req ObjectS3CredentialRotationRequest) (ObjectS3Credential, error) {
	if !validObjectS3CredentialRotationRequest(req) {
		return ObjectS3Credential{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	parent, err := q.ObjectS3CredentialRotationParentForUpdate(ctx, tx, sqlc.ObjectS3CredentialRotationParentForUpdateParams{
		ID: mustPgUUID(req.BindingID), AccountID: mustPgUUID(req.AccountID), BucketID: mustPgUUID(req.BucketID),
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if err := validateObjectS3CredentialRotationSecrets(req, objectS3CredentialFromSQL(parent)); err != nil {
		return ObjectS3Credential{}, err
	}
	if _, err := q.ObjectS3CredentialRotationPending(ctx, tx, sqlc.ObjectS3CredentialRotationPendingParams{
		AccountID: mustPgUUID(req.AccountID), BucketID: mustPgUUID(req.BucketID), RotationParentID: mustPgUUID(req.BindingID),
	}); err == nil {
		return ObjectS3Credential{}, ErrConflict
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectS3Credential{}, mapErr(err)
	}
	row, err := q.ObjectS3CredentialRotationReplace(ctx, tx, sqlc.ObjectS3CredentialRotationReplaceParams{
		ID: parent.ID, AccountID: parent.AccountID, BucketID: parent.BucketID,
		AccessKeyID: req.AccessKeyID, SecretSealed: req.SecretSealed, Kid: req.KID,
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	_, err = q.ObjectS3CredentialRotationStage(ctx, tx, sqlc.ObjectS3CredentialRotationStageParams{
		ID: mustPgUUID(uuid.NewString()), AccountID: parent.AccountID, BucketID: parent.BucketID,
		AccessKeyID: parent.AccessKeyID, SecretSealed: parent.SecretSealed, Kid: parent.Kid,
		Label: parent.Label, Permission: parent.Permission,
		RotationParentID: parent.ID, RotationWakeID: mustPgUUID(req.WakeID),
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	for _, secret := range req.Secrets {
		if err := lockAppSecretTarget(ctx, tx, secret.AppID, secret.Scope, secret.Key); err != nil {
			return ObjectS3Credential{}, err
		}
		n, err := q.ObjectStorageManagedSecretRotate(ctx, tx, sqlc.ObjectStorageManagedSecretRotateParams{
			AccountID: mustPgUUID(secret.AccountID), AppID: mustPgUUID(secret.AppID), Scope: secret.Scope,
			ManagedObjectStorageCredentialID: mustPgUUID(secret.ManagedObjectStorageCredentialID), Key: secret.Key,
			Ciphertext: secret.Ciphertext, Kid: pgtype.Text{String: secret.Kid, Valid: true},
			ValueHash: pgtype.Text{String: secret.ValueHash, Valid: true},
		})
		if err != nil {
			return ObjectS3Credential{}, mapErr(err)
		}
		if n != 1 {
			return ObjectS3Credential{}, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	return objectS3CredentialFromSQL(row), nil
}

func (s *PgStore) StampObjectS3CredentialRotation(ctx context.Context, appID, wakeID string) error {
	if _, err := uuid.Parse(appID); err != nil {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(wakeID); err != nil {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	stamp, err := q.ObjectS3CredentialRotationStampStage(ctx, tx, sqlc.ObjectS3CredentialRotationStampStageParams{
		ManagedAppID: mustPgUUID(appID), RotationWakeID: mustPgUUID(wakeID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return mapErr(err)
	}
	if err := q.ObjectS3CredentialRotationStampApp(ctx, tx, sqlc.ObjectS3CredentialRotationStampAppParams{
		AppID: mustPgUUID(appID), ChangedAt: stamp,
	}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

func (s *PgStore) FinalizeObjectS3CredentialRotationsForApp(ctx context.Context, appID, wakeID string) error {
	if appID == "" || wakeID == "" {
		return ErrInvalidArgument
	}
	_, err := sqlc.New().ObjectS3CredentialRotationFinalizeForApp(ctx, s.pool, sqlc.ObjectS3CredentialRotationFinalizeForAppParams{
		ManagedAppID: mustPgUUID(appID), RotationWakeID: mustPgUUID(wakeID),
	})
	return mapErr(err)
}

func (s *PgStore) ResolveObjectS3Credential(ctx context.Context, accessKeyID string) (ObjectS3Credential, ObjectBucket, error) {
	row, err := sqlc.New().ObjectS3CredentialResolve(ctx, s.pool, accessKeyID)
	if err != nil {
		return ObjectS3Credential{}, ObjectBucket{}, mapErr(err)
	}
	credential := ObjectS3Credential{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), BucketID: pgUUIDString(row.BucketID),
		AccessKeyID: row.AccessKeyID, SecretSealed: append([]byte(nil), row.SecretSealed...), KID: row.Kid,
		Label: row.Label, Permission: row.Permission, Status: row.Status, CreatedAt: row.CreatedAt.Time,
		LastUsedAt: optionalObjectS3Time(row.LastUsedAt), RevokedAt: optionalObjectS3Time(row.RevokedAt),
		ManagedAppID: pgUUIDStringNullable(row.ManagedAppID), ManagedScope: row.ManagedScope.String, ManagedPrefix: row.ManagedPrefix.String,
		RotationParentID: pgUUIDStringNullable(row.RotationParentID), RotationWakeID: pgUUIDStringNullable(row.RotationWakeID),
		RotationStampedAt: optionalObjectS3Time(row.RotationStampedAt),
	}
	bucket := ObjectBucket{
		ID: pgUUIDString(row.BucketID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
		Name: row.BucketName, Scope: row.BucketScope, Region: row.BucketRegion,
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, PhysicalName: row.PhysicalName,
		State: row.BucketState, CreatedAt: row.BucketCreatedAt.Time, UpdatedAt: row.BucketUpdatedAt.Time,
	}
	return credential, bucket, nil
}

func (s *PgStore) TouchObjectS3Credential(ctx context.Context, credentialID string, usedAt time.Time) error {
	n, err := sqlc.New().ObjectS3CredentialTouch(ctx, s.pool, sqlc.ObjectS3CredentialTouchParams{ID: mustPgUUID(credentialID), UsedAt: pgtype.Timestamptz{Time: usedAt.UTC(), Valid: true}})
	if err != nil {
		return mapErr(err)
	}
	// Zero rows is normal inside the one-minute write-coalescing window.
	_ = n
	return nil
}

func (s *PgStore) ListObjectS3CredentialsForRekey(ctx context.Context, limit int, afterID string) ([]ObjectS3Credential, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if afterID == "" {
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	rows, err := sqlc.New().ObjectS3CredentialListForRekey(ctx, s.pool, sqlc.ObjectS3CredentialListForRekeyParams{ID: mustPgUUID(afterID), BatchLimit: int32(limit)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectS3Credential, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectS3CredentialFromSQL(row))
	}
	return out, nil
}

func (s *PgStore) ResealObjectS3Credential(ctx context.Context, credentialID, previousKID, currentKID string, sealed []byte) error {
	if currentKID == "" || len(sealed) == 0 {
		return ErrInvalidArgument
	}
	n, err := sqlc.New().ObjectS3CredentialReseal(ctx, s.pool, sqlc.ObjectS3CredentialResealParams{
		ID: mustPgUUID(credentialID), Kid: previousKID, Kid_2: currentKID, SecretSealed: sealed,
	})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func objectS3CredentialFromSQL(row sqlc.ObjectStorageS3Credential) ObjectS3Credential {
	return ObjectS3Credential{
		ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), BucketID: pgUUIDString(row.BucketID),
		AccessKeyID: row.AccessKeyID, SecretSealed: append([]byte(nil), row.SecretSealed...), KID: row.Kid,
		Label: row.Label, Permission: row.Permission, Status: row.Status, CreatedAt: row.CreatedAt.Time,
		LastUsedAt: optionalObjectS3Time(row.LastUsedAt), RevokedAt: optionalObjectS3Time(row.RevokedAt),
		ManagedAppID: pgUUIDStringNullable(row.ManagedAppID), ManagedScope: row.ManagedScope.String, ManagedPrefix: row.ManagedPrefix.String,
		RotationParentID: pgUUIDStringNullable(row.RotationParentID), RotationWakeID: pgUUIDStringNullable(row.RotationWakeID),
		RotationStampedAt: optionalObjectS3Time(row.RotationStampedAt),
	}
}

func optionalObjectS3Time(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
