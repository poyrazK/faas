package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReadObjectMultipartMutation(ctx context.Context, u ObjectMultipartUpload) (ObjectBucketMutation, error) {
	if !validMultipartMutationScope(u) {
		return ObjectBucketMutation{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	// Match session admission: account, bucket, then journal.
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(u.AccountID)); err != nil {
		return ObjectBucketMutation{}, multipartMutationAuthorityError(err)
	}
	row, err := q.ObjectBucketMutationLock(ctx, tx, sqlc.ObjectBucketMutationLockParams{BucketID: mustPgUUID(u.BucketID), AccountID: mustPgUUID(u.AccountID), AppID: mustPgUUID(u.AppID)})
	if err != nil {
		return ObjectBucketMutation{}, multipartMutationAuthorityError(err)
	}
	b := objectBucketFromSQL(row)
	session, err := q.ObjectMultipartResultLock(ctx, tx, sqlc.ObjectMultipartResultLockParams{ID: mustPgUUID(u.ID), AccountID: mustPgUUID(u.AccountID), AppID: mustPgUUID(u.AppID), BucketID: mustPgUUID(u.BucketID)})
	if err != nil {
		return ObjectBucketMutation{}, multipartMutationAuthorityError(err)
	}
	old, err := objectMultipartFromSQL(session)
	if err != nil {
		return ObjectBucketMutation{}, err
	}
	if !originalMultipartMutationAuthority(old, u, time.Now()) {
		return ObjectBucketMutation{}, ErrConflict
	}
	receipt, err := q.ObjectMultipartMutationRead(ctx, tx, mustPgUUID(u.ID))
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	if pgUUIDString(receipt.BucketID) != u.BucketID || pgUUIDString(receipt.MultipartUploadID) != u.ID || receipt.Kind != ObjectBucketMutationRequest || b.State != "ready" || b.BackendID != receipt.BackendID || b.BackendFingerprint != receipt.BackendFingerprint || b.PhysicalName != receipt.PhysicalName {
		return ObjectBucketMutation{}, ErrConflict
	}
	out := ObjectBucketMutation{ID: pgUUIDString(receipt.ID), MultipartUploadID: u.ID, Bucket: b, Kind: receipt.Kind, CreatedAt: receipt.CreatedAt.Time}
	return out, mapErr(tx.Commit(ctx))
}

// ErrNotFound is reserved for an authenticated legacy journal with no binding.
// Missing scope or journal authority must never enter ordinary legacy admission.
func multipartMutationAuthorityError(err error) error {
	mapped := mapErr(err)
	if errors.Is(mapped, ErrNotFound) {
		return ErrConflict
	}
	return mapped
}
