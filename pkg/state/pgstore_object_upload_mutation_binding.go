package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ReadTrackedObjectUploadMutation(ctx context.Context, c ObjectUploadCompletion) (ObjectBucketMutation, error) {
	if !validUploadMutationScope(c) {
		return ObjectBucketMutation{}, ErrConflict
	}
	tx, old, err := s.lockTrackedObjectUpload(ctx, c)
	if err != nil {
		return ObjectBucketMutation{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if !originalUploadMutationAuthority(old, c, time.Now()) {
		return ObjectBucketMutation{}, ErrConflict
	}
	q := sqlc.New()
	r, err := q.ObjectTrackedUploadMutationRead(ctx, tx, mustPgUUID(old.ID))
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	row, err := q.ObjectBucketMutationLock(ctx, tx, sqlc.ObjectBucketMutationLockParams{BucketID: mustPgUUID(old.BucketID), AccountID: mustPgUUID(old.AccountID), AppID: mustPgUUID(old.AppID)})
	if err != nil {
		return ObjectBucketMutation{}, mapErr(err)
	}
	b := objectBucketFromSQL(row)
	if pgUUIDString(r.BucketID) != old.BucketID || pgUUIDString(r.UploadID) != old.ID || r.Kind != ObjectBucketMutationRequest || b.State != "ready" || b.BackendID != r.BackendID || b.BackendFingerprint != r.BackendFingerprint || b.PhysicalName != r.PhysicalName {
		return ObjectBucketMutation{}, ErrConflict
	}
	out := ObjectBucketMutation{ID: pgUUIDString(r.ID), UploadID: pgUUIDString(r.UploadID), Bucket: b, Kind: r.Kind, CreatedAt: r.CreatedAt.Time}
	return out, mapErr(tx.Commit(ctx))
}
