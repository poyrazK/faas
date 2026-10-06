package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartTransferStore = (*PgStore)(nil)

type multipartCompletionPreparation struct {
	protection      ObjectWriteProtectionSnapshot
	defaultRevision int64
	encryption      ObjectEncryptionSnapshot
	token           string
	revision        int64
	parts           []api.ObjectMultipartCompletedPart
	conditions      api.ObjectWriteConditions
	upload          ObjectMultipartUpload
}

func (s *PgStore) BeginObjectMultipartPart(ctx context.Context, account, bucket, id, token string, part int32, size, maxObject int64, p api.ObjectStoragePolicy) error {
	if token == "" || len(token) > 128 || part < 1 || part > api.MaxMultipartParts || size < 1 || size > api.MaxObjectSinglePutBytes || maxObject < 1 || maxObject > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	return s.admitMultipartCapacity(ctx, account, bucket, id, "", part, size, maxObject, p, token, nil, nil)
}

func (s *PgStore) SettleObjectMultipartPart(ctx context.Context, account, id string, part int32, token string) error {
	n, err := sqlc.New().ObjectMultipartPartSettle(ctx, s.pool, sqlc.ObjectMultipartPartSettleParams{UploadID: mustPgUUID(id), PartNumber: part, TransferToken: pgtype.Text{String: token, Valid: true}, AccountID: mustPgUUID(account)})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) PrepareObjectMultipartCompletion(ctx context.Context, u ObjectMultipartUpload, token string, size int64, parts []api.ObjectMultipartCompletedPart, p api.ObjectStoragePolicy) (ObjectMultipartUpload, error) {
	if !u.CompletionConditions.Valid() || token == "" || len(token) > 128 || len(parts) > api.MaxMultipartParts || size < 1 || size > api.MaxObjectUploadBytes || len(parts) == 0 {
		return ObjectMultipartUpload{}, ErrConflict
	}
	prep := multipartCompletionPreparation{protection: u.Protection.Clone(), defaultRevision: u.EncryptionDefaultRevision, encryption: u.Encryption, token: token, revision: u.PartRevision, parts: parts, conditions: u.CompletionConditions}
	err := s.admitMultipartCapacity(ctx, u.AccountID, u.BucketID, u.ID, u.Key, 0, size, 0, p, "", &prep, nil)
	return prep.upload, err
}

func (s *PgStore) ObjectMultipartAbortReady(ctx context.Context, id, token string) (bool, error) {
	q := sqlc.New()
	owner, err := q.ObjectMultipartAbortOwner(ctx, s.pool, sqlc.ObjectMultipartAbortOwnerParams{ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrConflict
	}
	if err != nil {
		return false, mapErr(err)
	}
	pending, err := q.ObjectMultipartTransfersPending(ctx, s.pool, mustPgUUID(id))
	return owner.PartUrlsDrained && !pending, err
}

// The caller must have verified an empty provider part list (or NoSuchUpload).
// Account-before-upload locking matches admission and makes quota release atomic.
func (s *PgStore) FinishVerifiedObjectMultipartAbort(ctx context.Context, id, token string) error {
	q := sqlc.New()
	owner, err := q.ObjectMultipartAbortOwner(ctx, s.pool, sqlc.ObjectMultipartAbortOwnerParams{ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrConflict
	}
	if err != nil {
		return mapErr(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = q.ObjectUsageLockAccount(ctx, tx, owner.AccountID); err != nil {
		return mapErr(err)
	}
	row, err := q.ObjectMultipartCapacityLock(ctx, tx, sqlc.ObjectMultipartCapacityLockParams{ID: mustPgUUID(id), AccountID: owner.AccountID, BucketID: owner.BucketID})
	if err != nil {
		return mapErr(err)
	}
	if row.State != ObjectMultipartAborting || row.LeaseToken.String != token || token == "" {
		return ErrConflict
	}
	pending, err := q.ObjectMultipartTransfersPending(ctx, tx, mustPgUUID(id))
	if err != nil {
		return err
	}
	if pending {
		return ErrConflict
	}
	n, err := q.ObjectMultipartFinishVerifiedAbort(ctx, tx, sqlc.ObjectMultipartFinishVerifiedAbortParams{ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if err = q.ObjectMultipartClearTransfers(ctx, tx, mustPgUUID(id)); err != nil {
		return err
	}
	if err = q.ObjectMultipartReleaseTrackedParts(ctx, tx, mustPgUUID(id)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) RejectObjectMultipartCompletion(ctx context.Context, id, token, code string) error {
	if token == "" || !validMultipartCompletionFailure(code) {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectMultipartRejectCompletion(ctx, s.pool, sqlc.ObjectMultipartRejectCompletionParams{ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}, CompletionErrorCode: code})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

var _ ObjectCrossBucketMultipartStore = (*PgStore)(nil)

func (s *PgStore) BeginObjectCrossBucketMultipartPart(ctx context.Context, account, bucket, id, token string, part int32, size, maxObject int64, p api.ObjectStoragePolicy, source ObjectMultipartCopySource) error {
	if token == "" || len(token) > 128 || !validMultipartCopyAuthority(source, bucket) || part < 1 || part > api.MaxMultipartParts || size < 1 || size > api.MaxObjectSinglePutBytes || maxObject < 1 || maxObject > api.MaxObjectUploadBytes {
		return ErrConflict
	}
	return s.admitMultipartCapacity(ctx, account, bucket, id, "", part, size, maxObject, p, token, nil, &source)
}
