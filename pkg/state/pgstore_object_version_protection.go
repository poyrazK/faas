package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectVersionProtectionStore = (*PgStore)(nil)

func readProtection(ctx context.Context, db sqlc.DBTX, id string) (ObjectVersionProtection, error) {
	r, err := sqlc.New().ObjectVersionProtectionGet(ctx, db, mustPgUUID(id))
	if err != nil {
		return ObjectVersionProtection{}, mapErr(err)
	}
	j := ObjectVersionProtection{AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), ProviderVersionID: r.NativeVersionID, Token: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time, Dispatched: r.Dispatched}
	err = json.Unmarshal(r.Intent, &j.ObjectVersionProtection)
	j.ID = pgUUIDString(r.ID)
	j.BucketID = pgUUIDString(r.BucketID)
	j.State = r.State
	j.LastErrorCode = r.LastErrorCode
	j.CreatedAt = r.CreatedAt.Time
	j.UpdatedAt = r.UpdatedAt.Time
	return j, err
}
func saveProtection(ctx context.Context, db sqlc.DBTX, j ObjectVersionProtection, initial bool) error {
	var lease pgtype.Timestamptz
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	if initial {
		intent, err := json.Marshal(j.ObjectVersionProtection)
		if err != nil {
			return err
		}
		return mapErr(sqlc.New().ObjectVersionProtectionInsert(ctx, db, sqlc.ObjectVersionProtectionInsertParams{ID: mustPgUUID(j.ID), BucketID: mustPgUUID(j.BucketID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID), ObjectKey: j.Key, PublicVersionID: j.VersionID, NativeVersionID: j.ProviderVersionID, Intent: intent}))
	}
	return mapErr(sqlc.New().ObjectVersionProtectionUpdate(ctx, db, sqlc.ObjectVersionProtectionUpdateParams{ID: mustPgUUID(j.ID), State: j.State, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), Dispatched: j.Dispatched, LastErrorCode: j.LastErrorCode}))
}
func (s *PgStore) BeginObjectVersionProtection(ctx context.Context, j ObjectVersionProtection) (ObjectVersionProtection, error) {
	if !ValidObjectVersionProtectionIntent(j) {
		return ObjectVersionProtection{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return j, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(j.AccountID)); err != nil {
		return j, mapErr(err)
	}
	if _, err = q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(j.BucketID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID)}); err != nil {
		return j, mapErr(err)
	}
	old, err := readProtection(ctx, tx, j.ID)
	if err == nil {
		if old.AccountID != j.AccountID || old.BucketID != j.BucketID {
			return ObjectVersionProtection{}, ErrNotFound
		}
		if !sameProtectionIntent(old, j) {
			return old, ErrConflict
		}
		return old, mapErr(tx.Commit(ctx))
	}
	if !errors.Is(err, ErrNotFound) {
		return j, err
	}
	active, err := q.ObjectVersionProtectionActive(ctx, tx, mustPgUUID(j.BucketID))
	if err == nil {
		old, e := readProtection(ctx, tx, pgUUIDString(active))
		if e != nil {
			return j, e
		}
		if !sameProtectionIntent(old, j) {
			return j, ErrConflict
		}
		return old, mapErr(tx.Commit(ctx))
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return j, err
	}
	fenced, err := q.ObjectCapacityFenced(ctx, tx, mustPgUUID(j.BucketID))
	if err != nil {
		return j, err
	}
	if fenced {
		return j, ErrConflict
	}
	encryption, err := readObjectBucketEncryption(ctx, tx, j.BucketID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return j, err
	}
	if encryption.State != "" && encryption.State != "ready" {
		return j, ErrConflict
	}
	lock, err := readObjectBucketObjectLock(ctx, tx, j.BucketID)
	if err != nil {
		return j, err
	}
	v, err := readObjectVersioning(ctx, tx, j.BucketID)
	if err != nil {
		return j, err
	}
	ready, err := objectLockDrainReady(ctx, tx, j.BucketID)
	if err != nil {
		return j, err
	}
	if !ready || !lock.NativeEnabledObserved || !lock.ObservedKnown || lock.ObservedConfiguration == nil || !lock.ObservedConfiguration.Enabled || !objectLockVersioningReady(v) {
		return j, ErrConflict
	}
	native := "null"
	if j.VersionID != "null" {
		native, err = q.ObjectVersionReferenceResolve(ctx, tx, sqlc.ObjectVersionReferenceResolveParams{ID: mustPgUUID(j.VersionID), AccountID: mustPgUUID(j.AccountID), BucketID: mustPgUUID(j.BucketID), ObjectKey: j.Key})
		if err != nil {
			return j, mapErr(err)
		}
	}
	now, err := q.ObjectVersioningNow(ctx, tx)
	if err != nil {
		return j, err
	}
	j = newProtectionIntent(j, native, now.Time)
	if err = saveProtection(ctx, tx, j, true); err != nil {
		return j, err
	}
	return j, mapErr(tx.Commit(ctx))
}
func (s *PgStore) GetObjectVersionProtection(ctx context.Context, account, bucket, id string) (ObjectVersionProtection, error) {
	if !ValidObjectVersionID(id) || id == "null" {
		return ObjectVersionProtection{}, ErrNotFound
	}
	j, err := readProtection(ctx, s.pool, id)
	if err != nil {
		return j, err
	}
	if j.AccountID != account || j.BucketID != bucket {
		return ObjectVersionProtection{}, ErrNotFound
	}
	b, err := s.GetObjectBucket(ctx, account, j.AppID, bucket)
	if err != nil {
		return ObjectVersionProtection{}, err
	}
	if b.State == "deleted" {
		return ObjectVersionProtection{}, ErrNotFound
	}
	return j, nil
}
func (s *PgStore) DueObjectVersionProtection(ctx context.Context, limit int32) ([]ObjectVersionProtection, error) {
	if limit < 1 || limit > api.ObjectVersionProtectionBatch {
		return nil, ErrConflict
	}
	ids, err := sqlc.New().ObjectVersionProtectionDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectVersionProtection, 0, len(ids))
	for _, id := range ids {
		j, e := readProtection(ctx, s.pool, pgUUIDString(id))
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *PgStore) mutateProtection(ctx context.Context, id string, fn func(ObjectVersionProtection, time.Time) (ObjectVersionProtection, error)) (ObjectVersionProtection, error) {
	if !ValidObjectVersionID(id) || id == "null" {
		return ObjectVersionProtection{}, ErrNotFound
	}
	old, err := readProtection(ctx, s.pool, id)
	if err != nil {
		return old, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return old, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(old.AccountID)); err != nil {
		return old, mapErr(err)
	}
	if _, err = q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(old.BucketID), AccountID: mustPgUUID(old.AccountID), AppID: mustPgUUID(old.AppID)}); err != nil {
		return old, mapErr(err)
	}
	j, err := readProtection(ctx, tx, id)
	if err != nil {
		return j, err
	}
	now, err := q.ObjectVersioningNow(ctx, tx)
	if err != nil {
		return j, err
	}
	j, err = fn(j, now.Time)
	if err != nil {
		return j, err
	}
	if err = saveProtection(ctx, tx, j, false); err != nil {
		return j, err
	}
	return j, mapErr(tx.Commit(ctx))
}
func (s *PgStore) ClaimObjectVersionProtection(ctx context.Context, id, token string) (ObjectVersionProtection, error) {
	return s.mutateProtection(ctx, id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		return claimProtection(j, token, now)
	})
}
func (s *PgStore) DispatchObjectVersionProtection(ctx context.Context, id, token string) (ObjectVersionProtection, error) {
	return s.mutateProtection(ctx, id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		if !validProtectionLease(j, token, now) || j.Dispatched {
			return j, ErrConflict
		}
		j.Dispatched = true
		j.UpdatedAt = now
		return j, nil
	})
}
func (s *PgStore) FinishObjectVersionProtection(ctx context.Context, id, token, status, code string) (ObjectVersionProtection, error) {
	return s.mutateProtection(ctx, id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		return finishProtection(j, token, status, code, now)
	})
}
func (s *PgStore) RetryObjectVersionProtection(ctx context.Context, id, token, code string) error {
	_, err := s.mutateProtection(ctx, id, func(j ObjectVersionProtection, now time.Time) (ObjectVersionProtection, error) {
		return retryProtection(j, token, code, now)
	})
	return err
}
