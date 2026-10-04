package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartCapacityStore = (*PgStore)(nil)

func (s *PgStore) AdmitObjectMultipartPart(ctx context.Context, account, bucket, id string, part int32, size, maxObject int64, p api.ObjectStoragePolicy) error {
	if part < 1 || part > api.MaxMultipartParts || size < 1 || size > api.MaxObjectSinglePutBytes || maxObject < 1 || maxObject > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	return s.admitMultipartCapacity(ctx, account, bucket, id, "", part, size, maxObject, p, "", nil, nil)
}
func (s *PgStore) AdmitObjectMultipartCompletion(ctx context.Context, account, bucket, id, key string, size int64, p api.ObjectStoragePolicy) error {
	if size < 1 || size > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	return s.admitMultipartCapacity(ctx, account, bucket, id, key, 0, size, 0, p, "", nil, nil)
}

func (s *PgStore) admitMultipartCapacity(ctx context.Context, account, bucket, id, key string, part int32, size, maxObject int64, p api.ObjectStoragePolicy, token string, preparation *multipartCompletionPreparation, source *ObjectMultipartCopySource) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return mapErr(err)
	}
	if source != nil {
		rows, e := q.ObjectCopySourceLockBuckets(ctx, tx, sqlc.ObjectCopySourceLockBucketsParams{AccountID: mustPgUUID(account), DestinationBucket: mustPgUUID(bucket), SourceBucket: mustPgUUID(source.BucketID)})
		if e != nil {
			return mapErr(e)
		}
		if len(rows) != 2 {
			return ErrConflict
		}
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
	if preparation != nil {
		pending, e := q.ObjectMultipartTransfersPending(ctx, tx, mustPgUUID(id))
		if e != nil {
			return e
		}
		if u.EncryptionDefaultRevision != preparation.defaultRevision || !u.Encryption.Equal(preparation.encryption) || pending || u.PartRevision != preparation.revision || ObjectMultipartIsCompleting(u.State) && (!slices.Equal(u.Parts, preparation.parts) || u.CompletionConditions != preparation.conditions || u.SizeBytes != size) {
			return ErrConflict
		}
	}
	if part != 0 && token != "" {
		transfer, e := q.ObjectMultipartPartTransfer(ctx, tx, sqlc.ObjectMultipartPartTransferParams{UploadID: mustPgUUID(id), PartNumber: part})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		if transfer.TransferToken.Valid && transfer.UnsafeUntil.Time.After(now) {
			return ErrConflict
		}
	}
	total, err := q.ObjectMultipartPartTotal(ctx, tx, mustPgUUID(id))
	if err != nil {
		return err
	}
	snapshot, err := readObjectUsage(ctx, tx, account, now)
	if err != nil {
		return err
	}
	if completion && u.FixedAdmission && size != u.SizeBytes {
		return ErrConflict
	}
	if completion && !u.FixedAdmission {
		snapshot = withoutMultipartReservation(snapshot, bucket, total)
		old, e := q.ObjectUsageGrant(ctx, tx, sqlc.ObjectUsageGrantParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key)})
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		all := versionAdmissionMode(snapshot, bucket)
		mode, e2 := q.ObjectVersionAccountingStatus(ctx, tx, sqlc.ObjectVersionAccountingStatusParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
		if e2 != nil {
			return mapErr(e2)
		}
		if mode.VersionsObserved && !all || all && preparation == nil {
			return ErrConflict
		}
		exists := e == nil
		if all {
			old = 0
			exists = false
		}
		delta, keys, e := checkObjectAdmission(snapshot, bucket, size, old, exists, true, p, now)
		if e != nil {
			return e
		}
		if preparation != nil {
			writeID := mustPgUUID(uuid.NewString())
			if err = q.ObjectWriteInsert(ctx, tx, sqlc.ObjectWriteInsertParams{ID: writeID, BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), Kind: "multipart", MultipartUploadID: mustPgUUID(id), NativeVersion: all, NativeBytes: nativeGrantBytes(all, size)}); err != nil {
				return err
			}
			if !all {
				err = q.ObjectTrackedGrantUpsert(ctx, tx, sqlc.ObjectTrackedGrantUpsertParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), MaxBytes: size, LastWriteID: writeID})
			}
		} else {
			err = q.ObjectUsageGrantUpsert(ctx, tx, sqlc.ObjectUsageGrantUpsertParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), MaxBytes: size})
		}
		if err != nil {
			return mapErr(err)
		}
		if err = q.ObjectUsageGrantIncrement(ctx, tx, sqlc.ObjectUsageGrantIncrementParams{BucketID: mustPgUUID(bucket), GrantedBytes: delta, GrantedKeys: keys}); err != nil {
			return err
		}
	} else if !completion {
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
		if token == "" {
			err = q.ObjectMultipartPartGrantUpsert(ctx, tx, sqlc.ObjectMultipartPartGrantUpsertParams{UploadID: mustPgUUID(id), PartNumber: part, MaxBytes: size})
		} else if source != nil {
			err = q.ObjectMultipartCopyPartBegin(ctx, tx, sqlc.ObjectMultipartCopyPartBeginParams{UploadID: mustPgUUID(id), PartNumber: part, MaxBytes: size, TransferToken: pgtype.Text{String: token, Valid: true}, WindowSeconds: int32(multipartTransferWindow() / time.Second), SourceBucketID: mustPgUUID(source.BucketID), SourceCopyGrantID: mustPgUUID(source.GrantID), SourceSubjectID: source.SubjectID, SourceKey: source.Key})
		} else {
			err = q.ObjectMultipartPartBegin(ctx, tx, sqlc.ObjectMultipartPartBeginParams{UploadID: mustPgUUID(id), PartNumber: part, MaxBytes: size, TransferToken: pgtype.Text{String: token, Valid: true}, Column5: int32(multipartTransferWindow() / time.Second)})
		}
		if err != nil {
			return mapErr(err)
		}
		if err = q.ObjectMultipartPartRevision(ctx, tx, mustPgUUID(id)); err != nil {
			return err
		}
	}
	if !completion || !u.FixedAdmission {
		if err = q.ObjectUsageAuthorize(ctx, tx, sqlc.ObjectUsageAuthorizeParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(ObjectStoragePeriod(now))}); err != nil {
			return err
		}
	}
	if preparation != nil {
		raw, e := multipartPartsJSON(preparation.parts)
		if e != nil {
			return e
		}
		row, e := q.ObjectMultipartClaim(ctx, tx, sqlc.ObjectMultipartClaimParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(u.AppID), BucketID: mustPgUUID(bucket), Operation: multipartCompletionOperation(preparation.conditions), CompletionIfMatch: preparation.conditions.IfMatch, CompletionIfNoneMatch: preparation.conditions.IfNoneMatch, Token: pgtype.Text{String: preparation.token, Valid: true}, LeaseSeconds: int32(ObjectMultipartLeaseDuration / time.Second), CompletionParts: raw})
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrConflict
		}
		if e != nil {
			return e
		}
		preparation.upload, e = objectMultipartFromSQL(row)
		if e != nil {
			return e
		}
		n, e := q.ObjectMultipartSetSize(ctx, tx, sqlc.ObjectMultipartSetSizeParams{ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: preparation.token, Valid: true}, SizeBytes: size})
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrConflict
		}
		preparation.upload.SizeBytes = size
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
