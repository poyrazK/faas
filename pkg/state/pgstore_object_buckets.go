package state

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

var _ ObjectBucketStore = (*PgStore)(nil)

func objectBucketFromSQL(b sqlc.ObjectBucket) ObjectBucket {
	return ObjectBucket{ID: pgUUIDString(b.ID), AccountID: pgUUIDString(b.AccountID), AppID: pgUUIDString(b.AppID), Name: b.Name, Scope: b.Scope, Region: b.Region, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName, State: b.State, CreatedAt: b.CreatedAt.Time, UpdatedAt: b.UpdatedAt.Time, LeaseToken: b.LeaseToken.String, LeaseUntil: b.LeaseUntil.Time}
}

func (s *PgStore) ReserveObjectBucket(ctx context.Context, b ObjectBucket, limit int) (ObjectBucket, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucket{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	// Serializes quota/name checks across replicas and with app deletion.
	_, err = q.ObjectBucketLockApp(ctx, tx, sqlc.ObjectBucketLockAppParams{ID: mustPgUUID(b.AppID), AccountID: mustPgUUID(b.AccountID)})
	if err != nil {
		return ObjectBucket{}, mapErr(err)
	}
	old, err := q.ObjectBucketByName(ctx, tx, sqlc.ObjectBucketByNameParams{AppID: mustPgUUID(b.AppID), AccountID: mustPgUUID(b.AccountID), Name: b.Name, Scope: b.Scope})
	if err == nil {
		return objectBucketFromSQL(old), tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectBucket{}, err
	}
	count, err := q.ObjectBucketCount(ctx, tx, mustPgUUID(b.AppID))
	if err != nil {
		return ObjectBucket{}, err
	}
	if count >= int64(limit) {
		return ObjectBucket{}, ErrConflict
	}
	out, err := q.ObjectBucketInsert(ctx, tx, sqlc.ObjectBucketInsertParams{ID: mustPgUUID(b.ID), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), Name: b.Name, Scope: b.Scope, Region: b.Region, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName})
	if err != nil {
		return ObjectBucket{}, mapErr(err)
	}
	return objectBucketFromSQL(out), tx.Commit(ctx)
}

func (s *PgStore) ListObjectBuckets(ctx context.Context, accountID, appID string) ([]ObjectBucket, error) {
	rows, err := sqlc.New().ObjectBucketList(ctx, s.pool, sqlc.ObjectBucketListParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectBucket, 0, len(rows))
	for _, b := range rows {
		out = append(out, objectBucketFromSQL(b))
	}
	return out, nil
}

func (s *PgStore) GetObjectBucket(ctx context.Context, accountID, appID, id string) (ObjectBucket, error) {
	b, err := sqlc.New().ObjectBucketGet(ctx, s.pool, sqlc.ObjectBucketGetParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ID: mustPgUUID(id)})
	return objectBucketFromSQL(b), mapErr(err)
}

func (s *PgStore) ClaimObjectBucket(ctx context.Context, accountID, appID, id, token, next string) (ObjectBucket, error) {
	if token == "" || (next != "provisioning" && next != "deleting") {
		return ObjectBucket{}, ErrConflict
	}
	b, err := sqlc.New().ObjectBucketClaim(ctx, s.pool, sqlc.ObjectBucketClaimParams{State: next, LeaseToken: pgtype.Text{String: token, Valid: true}, Column3: int32(ObjectBucketLeaseDuration / time.Second), AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), ID: mustPgUUID(id)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectBucket{}, ErrConflict
	}
	return objectBucketFromSQL(b), mapErr(err)
}

func (s *PgStore) FinishObjectBucket(ctx context.Context, id, token, next string) error {
	if token == "" || (next != "provisioning" && next != "ready" && next != "deleting" && next != "deleted") {
		return ErrConflict
	}
	count, err := sqlc.New().ObjectBucketFinish(ctx, s.pool, sqlc.ObjectBucketFinishParams{State: next, ID: mustPgUUID(id), LeaseToken: pgtype.Text{String: token, Valid: true}})
	if err != nil {
		return mapErr(err)
	}
	if count == 0 {
		return ErrConflict
	}
	return nil
}
