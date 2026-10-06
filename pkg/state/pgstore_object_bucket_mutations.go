package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectBucketWriteFenceStore = (*PgStore)(nil)

func lockObjectMutationBucket(ctx context.Context, tx pgx.Tx, b ObjectBucket) (ObjectBucket, error) {
	row, err := sqlc.New().ObjectBucketMutationLock(ctx, tx, sqlc.ObjectBucketMutationLockParams{
		BucketID: mustPgUUID(b.ID), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID)})
	if err != nil {
		return ObjectBucket{}, mapErr(err)
	}
	live := objectBucketFromSQL(row)
	if !sameObjectMutationBucket(b, live) {
		return ObjectBucket{}, ErrConflict
	}
	return live, nil
}

func (s *PgStore) BeginObjectBucketMutation(ctx context.Context, b ObjectBucket, kind string) (ObjectBucketMutation, error) {
	if !validObjectMutationBucket(b) || !validObjectMutationKind(kind) {
		return ObjectBucketMutation{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	b, err = lockObjectMutationBucket(ctx, tx, b)
	if err != nil {
		return ObjectBucketMutation{}, err
	}
	_, err = sqlc.New().ObjectBucketWriteFenceRead(ctx, tx, mustPgUUID(b.ID))
	if err == nil {
		return ObjectBucketMutation{}, ErrObjectBucketWriteFenced
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectBucketMutation{}, mapErr(err)
	}
	row, err := sqlc.New().ObjectBucketMutationInsert(ctx, tx, sqlc.ObjectBucketMutationInsertParams{
		ID: mustPgUUID(uuid.NewString()), BucketID: mustPgUUID(b.ID), Kind: kind,
		BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	out := ObjectBucketMutation{ID: pgUUIDString(row.ID), Bucket: b, Kind: row.Kind, CreatedAt: row.CreatedAt.Time}
	return out, mapErr(tx.Commit(ctx))
}

func (s *PgStore) FinishObjectBucketMutation(ctx context.Context, receipt ObjectBucketMutation) error {
	b := receipt.Bucket
	if !validObjectMutationBucket(b) || !validObjectMutationToken(receipt.ID) || receipt.Kind != ObjectBucketMutationRequest {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockObjectMutationBucket(ctx, tx, b); err != nil {
		return err
	}
	n, err := sqlc.New().ObjectBucketMutationFinish(ctx, tx, sqlc.ObjectBucketMutationFinishParams{
		ID: mustPgUUID(receipt.ID), BucketID: mustPgUUID(b.ID), BackendID: b.BackendID,
		BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}

func readObjectWriteFenceTx(ctx context.Context, tx pgx.Tx, b ObjectBucket, token string) (ObjectBucketWriteFence, error) {
	return readOwnedObjectWriteFenceTx(ctx, tx, b, token, "")
}

func readOwnedObjectWriteFenceTx(ctx context.Context, tx pgx.Tx, b ObjectBucket, token, operationID string) (ObjectBucketWriteFence, error) {
	row, err := sqlc.New().ObjectBucketWriteFenceRead(ctx, tx, mustPgUUID(b.ID))
	if err != nil {
		return ObjectBucketWriteFence{}, mapErr(err)
	}
	if pgUUIDString(row.Token) != token || pgUUIDString(row.CloneOperationID) != operationID || row.BackendID != b.BackendID || row.BackendFingerprint != b.BackendFingerprint || row.PhysicalName != b.PhysicalName {
		return ObjectBucketWriteFence{}, ErrConflict
	}
	return ObjectBucketWriteFence{Bucket: b, BucketID: b.ID, Token: token, CloneOperationID: operationID, Requests: row.Requests, NativeGrants: row.NativeGrants, Deletions: row.Deletions}, nil
}

func (s *PgStore) objectBucketWriteFence(ctx context.Context, b ObjectBucket, token string, acquire bool) (ObjectBucketWriteFence, error) {
	if !validObjectMutationBucket(b) || !validObjectMutationToken(token) {
		return ObjectBucketWriteFence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketWriteFence{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockObjectMutationBucket(ctx, tx, b); err != nil {
		return ObjectBucketWriteFence{}, err
	}
	if acquire {
		err = sqlc.New().ObjectBucketWriteFenceInsert(ctx, tx, sqlc.ObjectBucketWriteFenceInsertParams{
			BucketID: mustPgUUID(b.ID), Token: mustPgUUID(token), BackendID: b.BackendID,
			BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
		if err != nil {
			return ObjectBucketWriteFence{}, mapErr(err)
		}
	}
	fence, err := readObjectWriteFenceTx(ctx, tx, b, token)
	if err != nil {
		return ObjectBucketWriteFence{}, err
	}
	return fence, mapErr(tx.Commit(ctx))
}

func (s *PgStore) AcquireObjectBucketWriteFence(ctx context.Context, b ObjectBucket, token string) (ObjectBucketWriteFence, error) {
	return s.objectBucketWriteFence(ctx, b, token, true)
}

func (s *PgStore) ReadObjectBucketWriteFence(ctx context.Context, b ObjectBucket, token string) (ObjectBucketWriteFence, error) {
	return s.objectBucketWriteFence(ctx, b, token, false)
}

func (s *PgStore) ReleaseObjectBucketWriteFence(ctx context.Context, b ObjectBucket, token string) error {
	if !validObjectMutationBucket(b) || !validObjectMutationToken(token) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := lockObjectMutationBucket(ctx, tx, b); err != nil {
		return err
	}
	n, err := sqlc.New().ObjectBucketWriteFenceDelete(ctx, tx, sqlc.ObjectBucketWriteFenceDeleteParams{
		BucketID: mustPgUUID(b.ID), Token: mustPgUUID(token), BackendID: b.BackendID,
		BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
