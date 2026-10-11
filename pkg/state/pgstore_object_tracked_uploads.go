package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectTrackedUploadStore = (*PgStore)(nil)

func objectTrackedUploadFromSQL(r sqlc.ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	c := ObjectUploadCompletion{ID: pgUUIDString(r.ID), RouteID: pgUUIDString(r.RouteID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), BucketID: pgUUIDString(r.BucketID), SubjectID: r.SubjectID, Key: r.ObjectKey, Bytes: r.Bytes, ContentType: r.ContentType, ETag: r.Etag, Status: r.Status, ErrorCode: r.ErrorCode, RequestID: r.RequestID, IdempotencyKey: r.IdempotencyKey, RequestFingerprint: r.RequestFingerprint, CreatedAt: r.CreatedAt.Time, Origin: r.Origin, SourceKey: r.SourceKey, SourceETag: r.SourceEtag, SourceBucketID: pgUUIDString(r.SourceBucketID), SourceCopyGrantID: pgUUIDString(r.SourceCopyGrantID), WritePhase: r.WritePhase, RecoveryToken: r.RecoveryToken, RecoveryLeaseUntil: r.RecoveryLeaseUntil.Time, RecoveryRetryAt: r.RecoveryRetryAt.Time, RecoveryCursor: r.RecoveryCursor, RecoveryVersionsObserved: r.RecoveryVersionsObserved, VersionID: r.VersionID, EncryptionDefaultRevision: r.EncryptionDefaultRevision}
	var err error
	c.Encryption, err = encryptionSnapshotFromJSON(r.EncryptionSnapshot, c.AccountID)
	if err == nil {
		c.Protection, err = protectionSnapshotFromJSON(r.ProtectionSnapshot)
	}
	return c, err
}

func commitTrackedUploadSQL(ctx context.Context, tx pgx.Tx, r sqlc.ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	c, err := objectTrackedUploadFromSQL(r)
	if err != nil {
		return ObjectUploadCompletion{}, err
	}
	return c, tx.Commit(ctx)
}
func (s *PgStore) BeginTrackedObjectUpload(ctx context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, bool, error) {
	if c.EncryptionDefaultRevision != 0 || !validTrackedObjectUpload(c) {
		return c, false, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(c.AccountID)); err != nil {
		return c, false, mapErr(err)
	}
	if c.IdempotencyKey != "" {
		r, e := q.ObjectTrackedUploadReplay(ctx, tx, sqlc.ObjectTrackedUploadReplayParams{RouteID: mustPgUUID(c.RouteID), SubjectID: c.SubjectID, IdempotencyKey: c.IdempotencyKey, AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID)})
		if e == nil {
			out, e := commitTrackedUploadSQL(ctx, tx, r)
			return out, false, e
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return c, false, mapErr(e)
		}
	}
	route, err := q.ObjectUploadRouteForWrite(ctx, tx, sqlc.ObjectUploadRouteForWriteParams{ID: mustPgUUID(c.RouteID), AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID), BucketID: mustPgUUID(c.BucketID)})
	if err != nil {
		return c, false, mapErr(err)
	}
	routeEncryption, err := encryptionSnapshotFromJSON(route.EncryptionSnapshot, c.AccountID)
	if err != nil {
		return c, false, err
	}
	if c.Bytes > route.MaxBytes || !c.Encryption.Equal(routeEncryption) {
		return c, false, ErrConflict
	}
	if err := checkObjectUploadCaptureAdmissionTx(ctx, tx, c.AccountID, c.AppID, c.BucketID); err != nil {
		return c, false, err
	}
	c.Encryption, c.EncryptionDefaultRevision, err = captureObjectBucketDefaultSQL(ctx, tx, c.BucketID, c.Encryption)
	if err != nil {
		return c, false, err
	}
	if !capturedDefaultRouteFits(c) {
		return c, false, capturedDefaultRouteError(c)
	}
	c.Protection, err = captureObjectWriteProtectionSQL(ctx, tx, c.BucketID, c.Protection)
	if err != nil {
		return c, false, err
	}
	protection, err := protectionSnapshotJSON(c.Protection)
	if err != nil {
		return c, false, err
	}
	encryption, err := encryptionSnapshotJSON(c.Encryption)
	if err != nil {
		return c, false, err
	}
	if err = admitObjectURLTx(ctx, tx, c.AccountID, c.BucketID, c.Key, c.Bytes, true, p, c.ID, true); err != nil {
		return c, false, err
	}
	r, err := q.ObjectTrackedUploadInsert(ctx, tx, sqlc.ObjectTrackedUploadInsertParams{ID: mustPgUUID(c.ID), RouteID: mustPgUUID(c.RouteID), AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID), BucketID: mustPgUUID(c.BucketID), SubjectID: c.SubjectID, ObjectKey: c.Key, Bytes: c.Bytes, ContentType: c.ContentType, RequestID: c.RequestID, IdempotencyKey: c.IdempotencyKey, RequestFingerprint: c.RequestFingerprint, ProtectionSnapshot: protection, EncryptionSnapshot: encryption, EncryptionDefaultRevision: c.EncryptionDefaultRevision, RetrySeconds: int32(api.ObjectUploadPreparationTimeout / time.Second)})
	if err != nil {
		return c, false, mapErr(err)
	}
	out, e := commitTrackedUploadSQL(ctx, tx, r)
	return out, true, e
}
func (s *PgStore) DispatchTrackedObjectUpload(ctx context.Context, account, bucket, id string) (ObjectUploadCompletion, error) {
	r, err := sqlc.New().ObjectTrackedUploadDispatch(ctx, s.pool, sqlc.ObjectTrackedUploadDispatchParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), RetrySeconds: int32(api.ObjectUploadRecoveryRetry / time.Second)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectUploadCompletion{}, ErrConflict
	}
	if err != nil {
		return ObjectUploadCompletion{}, mapErr(err)
	}
	return objectTrackedUploadFromSQL(r)
}
func (s *PgStore) lockTrackedObjectUpload(ctx context.Context, c ObjectUploadCompletion) (pgx.Tx, ObjectUploadCompletion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, c, err
	}
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(c.AccountID)); err == nil {
		var r sqlc.ObjectUploadCompletion
		r, err = q.ObjectTrackedUploadGet(ctx, tx, sqlc.ObjectTrackedUploadGetParams{ID: mustPgUUID(c.ID), AccountID: mustPgUUID(c.AccountID), BucketID: mustPgUUID(c.BucketID)})
		if err == nil {
			c, err = objectTrackedUploadFromSQL(r)
		}
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, c, mapErr(err)
	}
	return tx, c, nil
}
func finishTrackedUploadSQL(ctx context.Context, tx pgx.Tx, old, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if !validTrackedEncryptionResult(old, c) {
		return old, ErrConflict
	}
	if old.WritePhase == ObjectUploadSettled {
		if old.Status == c.Status {
			return old, nil
		}
		return old, ErrConflict
	}
	if old.WritePhase != ObjectUploadPrepared && old.WritePhase != ObjectUploadDispatched || c.Status == "completed" && old.WritePhase != ObjectUploadDispatched {
		return old, ErrConflict
	}
	n, err := sqlc.New().ObjectRouteWriteSettle(ctx, tx, sqlc.ObjectRouteWriteSettleParams{ID: mustPgUUID(old.ID), BucketID: mustPgUUID(old.BucketID)})
	if err != nil {
		return old, err
	}
	if n != 1 {
		return old, ErrConflict
	}
	var version string
	if c.Status == "completed" && c.ProviderVersionID != "" {
		refs, e := recordObjectVersionsTx(ctx, tx, old.BucketID, []ObjectVersionIdentity{{Key: old.Key, ProviderVersionID: c.ProviderVersionID}})
		if e != nil {
			return old, e
		}
		version = refs[0].ID
	}
	r, err := sqlc.New().ObjectTrackedUploadFinish(ctx, tx, sqlc.ObjectTrackedUploadFinishParams{ID: mustPgUUID(old.ID), Status: c.Status, Etag: c.ETag, ErrorCode: c.ErrorCode, RecoveryVersionsObserved: c.RecoveryVersionsObserved || c.ProviderVersionID != "" && c.ProviderVersionID != "null", VersionID: version, ProtectionVerified: c.Status == "completed" && !old.Protection.Empty(), EncryptionVerified: c.Status == "completed" && !old.Encryption.Empty()})
	if err != nil {
		return old, mapErr(err)
	}
	out, err := objectTrackedUploadFromSQL(r)
	if err != nil {
		return old, err
	}
	if out.Status == "completed" {
		if err = publishObjectEventTx(ctx, tx, old.AccountID, "write:"+old.ID, api.ObjectEventCreated, trackedUploadEvent(out), time.Now()); err != nil {
			return old, err
		}
	}
	return out, nil
}
func (s *PgStore) FinishTrackedObjectUpload(ctx context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return s.finishTrackedObjectUpload(ctx, c, false)
}
func (s *PgStore) FinishTrackedObjectUploadRecovery(ctx context.Context, c ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return s.finishTrackedObjectUpload(ctx, c, true)
}
func (s *PgStore) finishTrackedObjectUpload(ctx context.Context, c ObjectUploadCompletion, recovery bool) (ObjectUploadCompletion, error) {
	if !validTrackedUploadFinish(c) || !validTrackedUploadCursor(c) {
		return c, ErrConflict
	}
	tx, old, err := s.lockTrackedObjectUpload(ctx, c)
	if err != nil {
		return old, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if recovery && (!validTrackedUploadRecovery(old, time.Now()) || old.RecoveryToken != c.RecoveryToken || c.Status != "completed") {
		return old, ErrConflict
	}
	out, err := finishTrackedUploadSQL(ctx, tx, old, c)
	if err != nil {
		return old, err
	}
	return out, tx.Commit(ctx)
}
func (s *PgStore) DueTrackedObjectUploads(ctx context.Context, limit int32) ([]ObjectUploadCompletion, error) {
	if limit < 1 || limit > api.ObjectUploadRecoveryBatch {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectTrackedUploadDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectUploadCompletion, 0, len(rows))
	for _, r := range rows {
		c, e := objectTrackedUploadFromSQL(r)
		if e != nil {
			return nil, e
		}
		out = append(out, c)
	}
	return out, nil
}
func (s *PgStore) ClaimTrackedObjectUploadRecovery(ctx context.Context, account, bucket, id, token string) (ObjectUploadCompletion, error) {
	tx, c, err := s.lockTrackedObjectUpload(ctx, ObjectUploadCompletion{ID: id, AccountID: account, BucketID: bucket})
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := time.Now()
	if token == "" || c.Status != "pending" || c.WritePhase == "untracked" || c.RecoveryRetryAt.After(now) || c.RecoveryLeaseUntil.After(now) {
		return c, ErrConflict
	}
	if c.WritePhase == ObjectUploadPrepared {
		done := c
		done.Status = "failed"
		done.ErrorCode = "preparation_expired"
		out, e := finishTrackedUploadSQL(ctx, tx, c, done)
		if e != nil {
			return c, e
		}
		return out, tx.Commit(ctx)
	}
	r, err := sqlc.New().ObjectTrackedUploadClaim(ctx, tx, sqlc.ObjectTrackedUploadClaimParams{ID: mustPgUUID(id), RecoveryToken: token, LeaseSeconds: int32(api.ObjectUploadRecoveryLease / time.Second)})
	if err != nil {
		return c, err
	}
	return commitTrackedUploadSQL(ctx, tx, r)
}
func (s *PgStore) RetryTrackedObjectUploadRecovery(ctx context.Context, c ObjectUploadCompletion, code string) error {
	if !validTrackedUploadRetry(code) || !validTrackedUploadCursor(c) {
		return ErrConflict
	}
	tx, old, err := s.lockTrackedObjectUpload(ctx, c)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if !old.Protection.Equal(c.Protection) || !sameCopySourceProvenance(old, c) || old.EncryptionDefaultRevision != c.EncryptionDefaultRevision || !old.Encryption.Equal(c.Encryption) || !validTrackedUploadRecovery(old, time.Now()) || old.RecoveryToken != c.RecoveryToken {
		return ErrConflict
	}
	err = sqlc.New().ObjectTrackedUploadRetry(ctx, tx, sqlc.ObjectTrackedUploadRetryParams{ID: mustPgUUID(c.ID), ErrorCode: code, RecoveryCursor: c.RecoveryCursor, RecoveryVersionsObserved: c.RecoveryVersionsObserved, RetrySeconds: int32(api.ObjectUploadRecoveryRetry / time.Second)})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) GetObjectUploadReceipt(ctx context.Context, account, app, route, subject, id string) (ObjectUploadCompletion, error) {
	r, err := sqlc.New().ObjectUploadReceiptGet(ctx, s.pool, sqlc.ObjectUploadReceiptGetParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), AppID: mustPgUUID(app), RouteID: mustPgUUID(route), SubjectID: subject})
	if err != nil {
		return ObjectUploadCompletion{}, mapErr(err)
	}
	return objectTrackedUploadFromSQL(r)
}

var _ ObjectTrackedGatewayUploadStore = (*PgStore)(nil)

func (s *PgStore) BeginTrackedGatewayUpload(ctx context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	if c.EncryptionDefaultRevision != 0 || !validTrackedGatewayUpload(c) {
		return c, ErrConflict
	}
	c.Origin = "gateway"
	return s.beginTrackedGatewayWrite(ctx, c, p)
}

var _ ObjectTrackedGatewayCopyStore = (*PgStore)(nil)

func (s *PgStore) BeginTrackedGatewayCopy(ctx context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	if c.EncryptionDefaultRevision != 0 || !validTrackedGatewayCopy(c) {
		return c, ErrConflict
	}
	c.Origin = "gateway_copy"
	return s.beginTrackedGatewayWrite(ctx, c, p)
}

func (s *PgStore) beginTrackedGatewayWrite(ctx context.Context, c ObjectUploadCompletion, p api.ObjectStoragePolicy) (ObjectUploadCompletion, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(c.AccountID)); err != nil {
		return c, mapErr(err)
	}
	if c.SourceBucketID != "" {
		buckets, e := q.ObjectCopySourceLockBuckets(ctx, tx, sqlc.ObjectCopySourceLockBucketsParams{AccountID: mustPgUUID(c.AccountID), DestinationBucket: mustPgUUID(c.BucketID), SourceBucket: mustPgUUID(c.SourceBucketID)})
		if e != nil {
			return c, mapErr(e)
		}
		if len(buckets) != 2 {
			return c, ErrConflict
		}
	}
	b, err := q.ObjectBucketGet(ctx, tx, sqlc.ObjectBucketGetParams{ID: mustPgUUID(c.BucketID), AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID)})
	if err != nil {
		return c, mapErr(err)
	}
	if b.State != "ready" {
		return c, ErrConflict
	}
	if err := checkObjectUploadCaptureAdmissionTx(ctx, tx, c.AccountID, c.AppID, c.BucketID); err != nil {
		return c, err
	}
	c.Encryption, c.EncryptionDefaultRevision, err = captureObjectBucketDefaultSQL(ctx, tx, c.BucketID, c.Encryption)
	if err != nil {
		return c, err
	}
	c.Protection, err = captureObjectWriteProtectionSQL(ctx, tx, c.BucketID, c.Protection)
	if err != nil {
		return c, err
	}
	protection, err := protectionSnapshotJSON(c.Protection)
	if err != nil {
		return c, err
	}
	encryption, err := encryptionSnapshotJSON(c.Encryption)
	if err != nil {
		return c, err
	}
	// Receipt-owned proxy journals keep old capacity workers aware of pending writes.
	if err = admitObjectURLTx(ctx, tx, c.AccountID, c.BucketID, c.Key, c.Bytes, true, p, c.ID, true); err != nil {
		return c, err
	}
	r, err := q.ObjectGatewayUploadInsert(ctx, tx, sqlc.ObjectGatewayUploadInsertParams{ID: mustPgUUID(c.ID), AccountID: mustPgUUID(c.AccountID), AppID: mustPgUUID(c.AppID), BucketID: mustPgUUID(c.BucketID), SubjectID: c.SubjectID, ObjectKey: c.Key, Bytes: c.Bytes, ContentType: c.ContentType, RequestID: c.RequestID, Origin: c.Origin, SourceKey: c.SourceKey, SourceEtag: c.SourceETag, SourceBucketID: mustPgUUID(c.SourceBucketID), SourceCopyGrantID: mustPgUUID(c.SourceCopyGrantID), ProtectionSnapshot: protection, EncryptionSnapshot: encryption, EncryptionDefaultRevision: c.EncryptionDefaultRevision, RetrySeconds: int32(api.ObjectUploadPreparationTimeout / time.Second)})
	if err != nil {
		return c, mapErr(err)
	}
	return commitTrackedUploadSQL(ctx, tx, r)
}

// Serialize creation of an original upload journal with source capture. Parts
// of an admitted multipart session do not call this new-journal admission seam.
func checkObjectUploadCaptureAdmissionTx(ctx context.Context, tx pgx.Tx, account, app, bucket string) error {
	q := sqlc.New()
	if _, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return mapErr(err)
	}
	_, err := q.ObjectBucketWriteFenceRead(ctx, tx, mustPgUUID(bucket))
	if err == nil {
		return ErrObjectBucketWriteFenced
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return mapErr(err)
}
