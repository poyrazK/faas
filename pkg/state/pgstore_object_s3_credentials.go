package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectS3CredentialStore = (*PgStore)(nil)
var _ ObjectS3CredentialRekeyStore = (*PgStore)(nil)

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
	})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ObjectS3Credential{}, err
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
	n, err := sqlc.New().ObjectS3CredentialTouch(ctx, s.pool, sqlc.ObjectS3CredentialTouchParams{ID: mustPgUUID(credentialID), LastUsedAt: pgtype.Timestamptz{Time: usedAt.UTC(), Valid: true}})
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
	}
}

func optionalObjectS3Time(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}
