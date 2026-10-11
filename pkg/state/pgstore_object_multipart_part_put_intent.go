package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectMultipartPartPutMutationStore = (*PgStore)(nil)

func (s *PgStore) DispatchObjectMultipartPartPutMutation(ctx context.Context, b ObjectBucket, id string, part int32, token string, i ObjectMultipartPartPutIntent) (ObjectBucketMutation, error) {
	if !validMultipartPartPutIntent(i) || i.BodySHA256 != "" {
		return ObjectBucketMutation{}, ErrConflict
	}
	raw, err := json.Marshal(i)
	if err != nil {
		return ObjectBucketMutation{}, ErrConflict
	}
	return s.dispatchMultipartPart(ctx, b, id, part, token, nil, raw)
}
func (s *PgStore) ObserveObjectMultipartPartBody(ctx context.Context, r ObjectBucketMutation, digest string) error {
	if !validMultipartPartReceipt(r) || !validMultipartPartSHA256(digest) {
		return ErrConflict
	}
	b := r.Bucket
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
		if errors.Is(err, ErrNotFound) {
			return ErrConflict
		}
		return err
	}
	n, err := q.ObjectMultipartPartBodyObserve(ctx, tx, sqlc.ObjectMultipartPartBodyObserveParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), BucketID: mustPgUUID(b.ID), BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName, BodySha256: digest})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return mapErr(tx.Commit(ctx))
}
func (s *PgStore) ReadObjectMultipartPartPutIntent(ctx context.Context, r ObjectBucketMutation) (ObjectMultipartPartPutIntent, error) {
	if !validMultipartPartReceipt(r) {
		return ObjectMultipartPartPutIntent{}, ErrConflict
	}
	b := r.Bucket
	row, err := sqlc.New().ObjectMultipartPartPutIntentRead(ctx, s.pool, sqlc.ObjectMultipartPartPutIntentReadParams{ID: mustPgUUID(r.ID), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), BucketID: mustPgUUID(b.ID), BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectMultipartPartPutIntent{}, ErrConflict
	}
	if err != nil {
		return ObjectMultipartPartPutIntent{}, mapErr(err)
	}
	var i ObjectMultipartPartPutIntent
	if json.Unmarshal(row.PutIntent, &i) != nil {
		return ObjectMultipartPartPutIntent{}, ErrConflict
	}
	i.BodySHA256 = row.BodySha256
	if !validMultipartPartPutIntent(i) {
		return ObjectMultipartPartPutIntent{}, ErrConflict
	}
	return i, nil
}
