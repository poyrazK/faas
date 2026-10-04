package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectS3CopySourceStore = (*PgStore)(nil)

func objectCopySourceFromSQL(g sqlc.ObjectS3CopySourceGrant) ObjectS3CopySource {
	return ObjectS3CopySource{ID: pgUUIDString(g.ID), AccountID: pgUUIDString(g.AccountID), BucketID: pgUUIDString(g.BucketID), CredentialID: pgUUIDString(g.CredentialID), SourceBucketID: pgUUIDString(g.SourceBucketID), Prefix: g.Prefix, CreatedAt: g.CreatedAt.Time, UpdatedAt: g.UpdatedAt.Time}
}

func (s *PgStore) ListObjectS3CopySources(ctx context.Context, account, bucket, credential string) ([]ObjectS3CopySource, error) {
	if _, err := s.GetObjectS3Credential(ctx, account, bucket, credential); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ObjectCopySourcesList(ctx, s.pool, sqlc.ObjectCopySourcesListParams{AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), CredentialID: mustPgUUID(credential)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectS3CopySource, 0, len(rows))
	for _, g := range rows {
		out = append(out, objectCopySourceFromSQL(g))
	}
	return out, nil
}

func (s *PgStore) SetObjectS3CopySource(ctx context.Context, account, bucket, credential, source, prefix string) (ObjectS3CopySource, error) {
	if source == bucket || !validObjectCopySourcePrefix(prefix) {
		return ObjectS3CopySource{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectS3CopySource{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return ObjectS3CopySource{}, mapErr(err)
	}
	buckets, err := q.ObjectCopySourceLockBuckets(ctx, tx, sqlc.ObjectCopySourceLockBucketsParams{AccountID: mustPgUUID(account), DestinationBucket: mustPgUUID(bucket), SourceBucket: mustPgUUID(source)})
	if err != nil {
		return ObjectS3CopySource{}, mapErr(err)
	}
	if len(buckets) != 2 {
		return ObjectS3CopySource{}, ErrNotFound
	}
	if !compatibleCopySourceBuckets(objectBucketFromSQL(buckets[0]), objectBucketFromSQL(buckets[1])) {
		return ObjectS3CopySource{}, ErrConflict
	}
	for _, id := range []string{bucket, source} {
		deleting, e := q.ObjectDeletionActive(ctx, tx, mustPgUUID(id))
		if e != nil {
			return ObjectS3CopySource{}, e
		}
		if deleting {
			return ObjectS3CopySource{}, ErrConflict
		}
	}
	r, err := q.ObjectCopySourceCredentialLock(ctx, tx, sqlc.ObjectCopySourceCredentialLockParams{ID: mustPgUUID(credential), AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket)})
	if err != nil {
		return ObjectS3CopySource{}, mapErr(err)
	}
	if !validCopySourceCredential(objectS3CredentialFromSQL(r)) {
		return ObjectS3CopySource{}, ErrConflict
	}
	old, err := q.ObjectCopySourceGet(ctx, tx, sqlc.ObjectCopySourceGetParams{CredentialID: mustPgUUID(credential), SourceBucketID: mustPgUUID(source)})
	if err == nil && old.Prefix == prefix {
		return objectCopySourceFromSQL(old), nil
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return ObjectS3CopySource{}, mapErr(err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		count, e := q.ObjectCopySourceCount(ctx, tx, mustPgUUID(credential))
		if e != nil {
			return ObjectS3CopySource{}, e
		}
		if count >= api.MaxObjectS3CopySourcesPerCredential {
			return ObjectS3CopySource{}, &ObjectStorageLimitError{Kind: "copy_sources_per_credential", Limit: api.MaxObjectS3CopySourcesPerCredential, Observed: count + 1, Cause: ErrConflict}
		}
	}
	g, err := q.ObjectCopySourceUpsert(ctx, tx, sqlc.ObjectCopySourceUpsertParams{ID: mustPgUUID(uuid.NewString()), AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), CredentialID: mustPgUUID(credential), SourceBucketID: mustPgUUID(source), Prefix: prefix})
	if err != nil {
		return ObjectS3CopySource{}, mapErr(err)
	}
	return objectCopySourceFromSQL(g), mapErr(tx.Commit(ctx))
}

func (s *PgStore) DeleteObjectS3CopySource(ctx context.Context, account, bucket, credential, source string) error {
	if _, err := s.GetObjectS3Credential(ctx, account, bucket, credential); err != nil {
		return err
	}
	n, err := sqlc.New().ObjectCopySourceDelete(ctx, s.pool, sqlc.ObjectCopySourceDeleteParams{AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), CredentialID: mustPgUUID(credential), SourceBucketID: mustPgUUID(source)})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ResolveObjectS3CopySource(ctx context.Context, account, credential, source, key string) (ObjectS3CopySource, ObjectBucket, error) {
	if key == "" || !validObjectCopySourcePrefix(key) {
		return ObjectS3CopySource{}, ObjectBucket{}, ErrNotFound
	}
	r, err := sqlc.New().ObjectCopySourceResolve(ctx, s.pool, sqlc.ObjectCopySourceResolveParams{AccountID: mustPgUUID(account), CredentialID: mustPgUUID(credential), SourceBucketID: mustPgUUID(source), ObjectKey: key})
	if err != nil {
		return ObjectS3CopySource{}, ObjectBucket{}, mapErr(err)
	}
	return objectCopySourceFromSQL(r.ObjectS3CopySourceGrant), objectBucketFromSQL(r.ObjectBucket), nil
}

func (s *PgStore) GetObjectS3CopySourceBucket(ctx context.Context, account, id string) (ObjectBucket, error) {
	b, err := sqlc.New().ObjectCopySourceOwnedBucket(ctx, s.pool, sqlc.ObjectCopySourceOwnedBucketParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ObjectBucket{}, mapErr(err)
	}
	return objectBucketFromSQL(b), nil
}
