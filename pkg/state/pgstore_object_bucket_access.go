package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectBucketAccessStore = (*PgStore)(nil)

func (s *PgStore) ListObjectBucketAccessGrants(ctx context.Context, accountID, bucketID string) ([]ObjectBucketAccessGrant, error) {
	rows, err := sqlc.New().ObjectBucketAccessGrantList(ctx, s.pool, sqlc.ObjectBucketAccessGrantListParams{
		AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectBucketAccessGrant, 0, len(rows))
	for _, row := range rows {
		out = append(out, ObjectBucketAccessGrant{
			AccountID: pgUUIDString(row.AccountID), BucketID: pgUUIDString(row.BucketID),
			APIKeyID: pgUUIDString(row.ApiKeyID), Permission: row.Permission,
			KeyLabel: row.KeyLabel, KeyStatus: row.KeyStatus,
			CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		})
	}
	return out, nil
}

func (s *PgStore) SetObjectBucketAccessGrant(ctx context.Context, accountID, bucketID, keyID, permission string) (ObjectBucketAccessGrant, error) {
	if !validObjectBucketPermission(permission) {
		return ObjectBucketAccessGrant{}, ErrConflict
	}
	q := sqlc.New()
	n, err := q.ObjectBucketAccessGrantUpsert(ctx, s.pool, sqlc.ObjectBucketAccessGrantUpsertParams{
		AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID),
		ApiKeyID: mustPgUUID(keyID), Permission: permission,
	})
	if err != nil {
		return ObjectBucketAccessGrant{}, mapErr(err)
	}
	if n == 0 {
		return ObjectBucketAccessGrant{}, ErrConflict
	}
	row, err := q.ObjectBucketAccessGrantGet(ctx, s.pool, sqlc.ObjectBucketAccessGrantGetParams{
		AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID), ApiKeyID: mustPgUUID(keyID),
	})
	if err != nil {
		return ObjectBucketAccessGrant{}, mapErr(err)
	}
	return ObjectBucketAccessGrant{
		AccountID: pgUUIDString(row.AccountID), BucketID: pgUUIDString(row.BucketID),
		APIKeyID: pgUUIDString(row.ApiKeyID), Permission: row.Permission,
		KeyLabel: row.KeyLabel, KeyStatus: row.KeyStatus,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
	}, nil
}

func (s *PgStore) DeleteObjectBucketAccessGrant(ctx context.Context, accountID, bucketID, keyID string) error {
	n, err := sqlc.New().ObjectBucketAccessGrantDelete(ctx, s.pool, sqlc.ObjectBucketAccessGrantDeleteParams{
		AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID), ApiKeyID: mustPgUUID(keyID),
	})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ObjectBucketKeyCan(ctx context.Context, accountID, bucketID, keyID, permission string) (bool, error) {
	if permission != ObjectBucketPermissionRead && permission != ObjectBucketPermissionWrite {
		return false, ErrConflict
	}
	allowed, err := sqlc.New().ObjectBucketAccessCheck(ctx, s.pool, sqlc.ObjectBucketAccessCheckParams{
		AccountID: mustPgUUID(accountID), BucketID: mustPgUUID(bucketID),
		ApiKeyID: mustPgUUID(keyID), Column4: permission,
	})
	return allowed, mapErr(err)
}

func (s *PgStore) ListObjectBucketsForKey(ctx context.Context, accountID, appID, keyID string) ([]ObjectBucket, error) {
	rows, err := sqlc.New().ObjectBucketListForKey(ctx, s.pool, sqlc.ObjectBucketListForKeyParams{
		AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ApiKeyID: mustPgUUID(keyID),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectBucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectBucketFromSQL(row))
	}
	return out, nil
}
