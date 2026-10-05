package state

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectURLCapabilityStore = (*PgStore)(nil)

func (s *PgStore) DispatchObjectURLUpload(ctx context.Context, account, bucket, id string) (ObjectUploadCompletion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectUploadCompletion{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectURLCredentialForReceipt(ctx, tx, sqlc.ObjectURLCredentialForReceiptParams{AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), UrlReceiptID: mustPgUUID(id)}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ObjectUploadCompletion{}, ErrConflict
		}
		return ObjectUploadCompletion{}, mapErr(err)
	}
	row, err := q.ObjectTrackedUploadDispatch(ctx, tx, sqlc.ObjectTrackedUploadDispatchParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), RetrySeconds: int32(api.ObjectUploadRecoveryRetry / time.Second)})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ObjectUploadCompletion{}, ErrConflict
		}
		return ObjectUploadCompletion{}, mapErr(err)
	}
	if err = q.ObjectStorageProviderRequestIncrement(ctx, tx, sqlc.ObjectStorageProviderRequestIncrementParams{BucketID: mustPgUUID(bucket), PeriodStart: objectUsageTime(ObjectStoragePeriod(time.Now().UTC()))}); err != nil {
		return ObjectUploadCompletion{}, mapErr(err)
	}
	return commitTrackedUploadSQL(ctx, tx, row)
}

func optionalURLUUID(id string) pgtype.UUID {
	if id == "" {
		return pgtype.UUID{}
	}
	return mustPgUUID(id)
}

func (s *PgStore) IssueObjectURLCredential(ctx context.Context, c ObjectS3Credential, receipt ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectS3Credential, ObjectUploadCompletion, error) {
	if !receipt.Protection.ValidInput() || receipt.VerifiedProtection != "" || receipt.EncryptionDefaultRevision != 0 || !validObjectURLCredential(c, receipt, time.Now()) {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(c.AccountID)); err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, mapErr(err)
	}
	if _, err = q.ObjectURLCredentialLockBucket(ctx, tx, sqlc.ObjectURLCredentialLockBucketParams{ID: mustPgUUID(c.BucketID), AccountID: mustPgUUID(c.AccountID)}); err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, mapErr(err)
	}
	if c.URL.Request.Method == http.MethodPut {
		_, fenceErr := q.ObjectBucketWriteFenceRead(ctx, tx, mustPgUUID(c.BucketID))
		if fenceErr == nil {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrObjectBucketWriteFenced
		}
		if !errors.Is(fenceErr, pgx.ErrNoRows) {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, mapErr(fenceErr)
		}
	}
	if c.URL.Request.Method == http.MethodPut {
		receipt.Encryption, receipt.EncryptionDefaultRevision, err = captureObjectBucketDefaultSQL(ctx, tx, receipt.BucketID, receipt.Encryption)
		if err != nil {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, err
		}
		receipt.Protection, err = captureObjectWriteProtectionSQL(ctx, tx, receipt.BucketID, receipt.Protection)
		if err != nil {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, err
		}
		c = bindObjectURLDefault(c, receipt)
		if !validObjectURLReceipt(c, receipt) {
			return ObjectS3Credential{}, ObjectUploadCompletion{}, ErrConflict
		}
	}
	out, err := insertObjectURLCredentialSQL(ctx, tx, c)
	if err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, mapErr(err)
	}
	if c.URL.Request.Method == http.MethodPut {
		receipt, err = issueObjectURLWriteSQL(ctx, tx, c, receipt, p)
	} else {
		err = admitObjectURLTx(ctx, tx, c.AccountID, c.BucketID, c.URL.Request.Key, 0, false, p, "", false)
	}
	if err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, mapErr(err)
	}
	if err = tx.Commit(ctx); err != nil {
		return ObjectS3Credential{}, ObjectUploadCompletion{}, mapErr(err)
	}
	return out, receipt, nil
}

func insertObjectURLCredentialSQL(ctx context.Context, tx pgx.Tx, c ObjectS3Credential) (ObjectS3Credential, error) {
	request, err := objectURLRequestJSON(c.URL)
	if err != nil {
		return ObjectS3Credential{}, err
	}
	q := sqlc.New()
	if err = q.ObjectURLCredentialCleanup(ctx, tx, sqlc.ObjectURLCredentialCleanupParams{BucketID: mustPgUUID(c.BucketID), BatchLimit: api.MaxObjectURLCapabilitiesPerBucket}); err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	n, err := q.ObjectURLCredentialCount(ctx, tx, mustPgUUID(c.BucketID))
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	if n >= api.MaxObjectURLCapabilitiesPerBucket {
		return ObjectS3Credential{}, objectURLCapabilityLimitError(n)
	}
	row, err := q.ObjectURLCredentialInsert(ctx, tx, sqlc.ObjectURLCredentialInsertParams{ID: mustPgUUID(c.ID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID), AccessKeyID: c.AccessKeyID, SecretSealed: c.SecretSealed, Kid: c.KID, Label: c.Label, Permission: c.Permission, UrlRequest: request, UrlApiKeyID: optionalURLUUID(c.URL.APIKeyID), UrlReceiptID: optionalURLUUID(c.URL.ReceiptID), UrlExpiresAt: pgtype.Timestamptz{Time: c.URL.ExpiresAt, Valid: true}})
	if err != nil {
		return ObjectS3Credential{}, mapErr(err)
	}
	out := objectS3CredentialFromSQL(row)
	out.URL, err = objectURLCapabilityFromJSON(row.UrlRequest, pgUUIDStringNullable(row.UrlApiKeyID), pgUUIDStringNullable(row.UrlReceiptID), row.UrlExpiresAt.Time)
	return out, err
}

func issueObjectURLWriteSQL(ctx context.Context, tx pgx.Tx, credential ObjectS3Credential, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	q := sqlc.New()
	b, err := q.ObjectBucketGet(ctx, tx, sqlc.ObjectBucketGetParams{ID: mustPgUUID(c.BucketID), AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID)})
	if err != nil {
		return c, mapErr(err)
	}
	if b.State != "ready" {
		return c, ErrConflict
	}
	protection, err := protectionSnapshotJSON(c.Protection)
	if err != nil {
		return c, err
	}
	encryption, err := encryptionSnapshotJSON(c.Encryption)
	if err != nil {
		return c, err
	}
	if err = admitObjectURLTx(ctx, tx, c.AccountID, c.BucketID, c.Key, c.Bytes, true, p, c.ID, true); err != nil {
		return c, err
	}
	row, err := q.ObjectGatewayUploadInsert(ctx, tx, sqlc.ObjectGatewayUploadInsertParams{ID: mustPgUUID(c.ID), AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID), BucketID: mustPgUUID(c.BucketID), SubjectID: c.SubjectID, ObjectKey: c.Key, Bytes: c.Bytes, ContentType: c.ContentType, RequestID: c.RequestID, Origin: "gateway", ProtectionSnapshot: protection, EncryptionSnapshot: encryption, EncryptionDefaultRevision: c.EncryptionDefaultRevision, RetrySeconds: int32(credential.URL.Request.ExpiresIn)})
	if err != nil {
		return c, mapErr(err)
	}
	return objectTrackedUploadFromSQL(row)
}
