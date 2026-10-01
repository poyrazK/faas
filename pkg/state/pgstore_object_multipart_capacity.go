package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartCapacityStore = (*PgStore)(nil)

func (s *PgStore) AdmitObjectMultipartPart(ctx context.Context, account, bucket, id string, part int32, size, maxObject int64, p api.ObjectStoragePolicy) error {
	if part < 1 || part > api.MaxMultipartParts || size < 1 || size > api.MaxObjectSinglePutBytes || maxObject < 1 || maxObject > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	return s.admitMultipartCapacity(ctx, account, bucket, id, "", part, size, maxObject, p)
}
func (s *PgStore) AdmitObjectMultipartCompletion(ctx context.Context, account, bucket, id, key string, size int64, p api.ObjectStoragePolicy) error {
	if size < 1 || size > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	return s.admitMultipartCapacity(ctx, account, bucket, id, key, 0, size, 0, p)
}

func (s *PgStore) admitMultipartCapacity(ctx context.Context, account, bucket, id, key string, part int32, size, maxObject int64, p api.ObjectStoragePolicy) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return mapErr(err)
	}
	row, err := q.ObjectMultipartCapacityLock(ctx, tx, sqlc.ObjectMultipartCapacityLockParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket)})
	if err != nil {
		return mapErr(err)
	}
	u, err := objectMultipartFromSQL(row)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	completion := part == 0
	if !validMultipartCapacityUpload(u, account, bucket, completion, now) || completion && u.Key != key {
		return ErrConflict
	}
	total, err := q.ObjectMultipartPartTotal(ctx, tx, mustPgUUID(id))
	if err != nil {
		return err
	}
	snapshot, err := readObjectUsage(ctx, tx, account, now)
	if err != nil {
		return err
	}
	if completion {
		snapshot = withoutMultipartReservation(snapshot, bucket, total)
		old, e := q.ObjectUsageGrant(ctx, tx, sqlc.ObjectUsageGrantParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key)})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		delta, keys, e := checkObjectAdmission(snapshot, bucket, size, old, e == nil, true, p, now)
		if e != nil {
			return e
		}
		if err = q.ObjectUsageGrantUpsert(ctx, tx, sqlc.ObjectUsageGrantUpsertParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), MaxBytes: size}); err != nil {
			return err
		}
		if err = q.ObjectUsageGrantIncrement(ctx, tx, sqlc.ObjectUsageGrantIncrementParams{BucketID: mustPgUUID(bucket), GrantedBytes: delta, GrantedKeys: keys}); err != nil {
			return err
		}
	} else {
		old, e := q.ObjectMultipartPartGrant(ctx, tx, sqlc.ObjectMultipartPartGrantParams{UploadID: mustPgUUID(id), PartNumber: part})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		delta := max(int64(0), size-old)
		if total > maxObject-delta {
			return ErrObjectCapacity
		}
		if _, _, err = checkObjectAdmission(snapshot, bucket, delta, 0, true, true, p, now); err != nil {
			return err
		}
		if err = q.ObjectMultipartPartGrantUpsert(ctx, tx, sqlc.ObjectMultipartPartGrantUpsertParams{UploadID: mustPgUUID(id), PartNumber: part, MaxBytes: size}); err != nil {
			return err
		}
	}
	if err = q.ObjectUsageAuthorize(ctx, tx, sqlc.ObjectUsageAuthorizeParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(ObjectStoragePeriod(now))}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) ListObjectS3MultipartUploads(ctx context.Context, account, app, bucket, prefix, keyMarker, uploadMarker string, limit int32) ([]ObjectMultipartUpload, error) {
	if limit < 1 || limit > api.MaxObjectS3ListItems+1 {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectS3MultipartList(ctx, s.pool, sqlc.ObjectS3MultipartListParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), BucketID: mustPgUUID(bucket), Prefix: prefix, KeyMarker: keyMarker, UploadMarker: uploadMarker, PageLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]ObjectMultipartUpload, 0, len(rows))
	for _, r := range rows {
		u, e := objectMultipartFromSQL(r)
		if e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, nil
}
