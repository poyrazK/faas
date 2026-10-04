package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartURLCapabilityStore = (*PgStore)(nil)

func (s *PgStore) IssueObjectMultipartURLCredential(ctx context.Context, c ObjectS3Credential, expected ObjectMultipartUpload, p api.ObjectStoragePolicy) (ObjectS3Credential, error) {
	if !validObjectURLMultipartUpload(c, expected, time.Now()) {
		return ObjectS3Credential{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(c.AccountID)); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if _, err = q.ObjectS3CredentialLockBucket(ctx, tx, sqlc.ObjectS3CredentialLockBucketParams{ID: mustPgUUID(c.BucketID), AccountID: mustPgUUID(c.AccountID)}); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	row, err := q.ObjectMultipartCapacityLock(ctx, tx, sqlc.ObjectMultipartCapacityLockParams{ID: mustPgUUID(expected.ID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID)})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	u, err := objectMultipartFromSQL(row)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	if !validObjectURLMultipartUpload(c, u, time.Now()) || u.AppID != expected.AppID || u.ProviderUploadID != expected.ProviderUploadID || !u.Encryption.Equal(expected.Encryption) {
		return ObjectS3Credential{}, ErrConflict
	}
	out, err := insertObjectURLCredentialSQL(ctx, tx, c)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	if err = admitObjectURLTx(ctx, tx, c.AccountID, c.BucketID, c.URL.Request.Key, 0, false, p, "", false); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	return out, nil
}

func multipartURLCredentialFromSQL(row sqlc.ObjectStorageS3Credential) (ObjectS3Credential, error) {
	c := objectS3CredentialFromSQL(row)
	var err error
	c.URL, err = objectURLCapabilityFromJSON(row.UrlRequest, pgUUIDStringNullable(row.UrlApiKeyID), pgUUIDStringNullable(row.UrlReceiptID), row.UrlExpiresAt.Time)
	return c, err
}

func (s *PgStore) BeginObjectURLMultipartPart(ctx context.Context, id, token string, p api.ObjectStoragePolicy) error {
	if token == "" || len(token) > 128 {
		return ErrConflict
	}
	q := sqlc.New()
	row, err := q.ObjectURLMultipartCredential(ctx, s.pool, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	c, err := multipartURLCredentialFromSQL(row)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(c.AccountID)); err != nil {
		return mapErr(err)
	}
	row, err = q.ObjectURLMultipartCredential(ctx, tx, mustPgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	c, err = multipartURLCredentialFromSQL(row)
	if err != nil {
		return err
	}
	urow, err := q.ObjectMultipartCapacityLock(ctx, tx, sqlc.ObjectMultipartCapacityLockParams{ID: mustPgUUID(c.URL.Multipart.UploadID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID)})
	if err != nil {
		return mapErr(err)
	}
	u, err := objectMultipartFromSQL(urow)
	if err != nil {
		return err
	}
	if !validObjectURLMultipartUpload(c, u, time.Now()) {
		return ErrConflict
	}
	part := c.URL.Multipart.PartNumber
	prior, err := q.ObjectMultipartPartTransfer(ctx, tx, sqlc.ObjectMultipartPartTransferParams{UploadID: mustPgUUID(u.ID), PartNumber: part})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return mapErr(err)
	}
	if prior.TransferToken.Valid && prior.UnsafeUntil.Time.After(time.Now()) {
		return ErrConflict
	}
	if err = admitObjectURLTx(ctx, tx, c.AccountID, c.BucketID, c.URL.Request.Key, 0, false, p, "", false); err != nil {
		return mapErr(err)
	}
	if err = q.ObjectMultipartURLPartBegin(ctx, tx, sqlc.ObjectMultipartURLPartBeginParams{UploadID: mustPgUUID(u.ID), PartNumber: part, TransferToken: pgtype.Text{String: token, Valid: true}, WindowSeconds: int32(multipartTransferWindow() / time.Second), UrlCredentialID: mustPgUUID(c.ID)}); err != nil {
		return mapErr(err)
	}
	if err = q.ObjectMultipartPartRevision(ctx, tx, mustPgUUID(u.ID)); err != nil {
		return mapErr(err)
	}
	if err = q.ObjectStorageProviderRequestIncrement(ctx, tx, sqlc.ObjectStorageProviderRequestIncrementParams{BucketID: mustPgUUID(u.BucketID), PeriodStart: objectUsageTime(ObjectStoragePeriod(time.Now()))}); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
