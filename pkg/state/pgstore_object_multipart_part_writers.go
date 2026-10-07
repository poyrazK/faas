package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartPartMutationStore = (*PgStore)(nil)

func (s *PgStore) DispatchObjectMultipartPartMutation(ctx context.Context, b ObjectBucket, id string, part int32, token string) (ObjectBucketMutation, error) {
	if !validObjectMutationBucket(b) || !validObjectMutationToken(id) || part < 1 || part > api.MaxMultipartParts || token == "" || len(token) > 128 {
		return ObjectBucketMutation{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketMutation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(b.AccountID)); err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	if _, err = lockObjectMutationBucket(ctx, tx, b); err != nil {
		return ObjectBucketMutation{}, err
	}
	d, err := q.ObjectMultipartPartWriterDispatch(ctx, tx, sqlc.ObjectMultipartPartWriterDispatchParams{UploadID: mustPgUUID(id), PartNumber: part, TransferToken: token, AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), BucketID: mustPgUUID(b.ID), BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectBucketMutation{}, ErrConflict
	}
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	writerID := pgUUIDString(d.ID)
	return ObjectBucketMutation{ID: writerID, MultipartPartWriterID: writerID, Bucket: b, Kind: ObjectBucketMutationRequest}, mapErr(tx.Commit(ctx))
}

func (s *PgStore) FinishObjectMultipartPartMutation(ctx context.Context, r ObjectBucketMutation) error {
	b := r.Bucket
	if !validObjectMutationBucket(b) || !validObjectMutationToken(r.ID) || r.ID != r.MultipartPartWriterID || r.UploadID != "" || r.MultipartUploadID != "" || r.Kind != ObjectBucketMutationRequest {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(b.AccountID)); err != nil {
		return mapErr(err)
	}
	if _, err = lockObjectMutationBucket(ctx, tx, b); err != nil {
		return err
	}
	n, err := q.ObjectMultipartPartWriterFinish(ctx, tx, sqlc.ObjectMultipartPartWriterFinishParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), BucketID: mustPgUUID(b.ID), BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
