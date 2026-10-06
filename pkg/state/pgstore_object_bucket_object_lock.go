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

var _ ObjectBucketObjectLockStore = (*PgStore)(nil)

func readObjectBucketObjectLock(ctx context.Context, db sqlc.DBTX, bucket string) (ObjectBucketObjectLock, error) {
	r, err := sqlc.New().ObjectBucketObjectLockGet(ctx, db, mustPgUUID(bucket))
	if err != nil {
		return ObjectBucketObjectLock{}, mapErr(err)
	}
	j := ObjectBucketObjectLock{ObjectBucketObjectLock: api.ObjectBucketObjectLock{BucketID: bucket, State: r.State, Revision: r.Revision, EnabledRequired: r.EnabledRequired, ObservedKnown: r.ObservedKnown, LastErrorCode: r.LastErrorCode, UpdatedAt: r.UpdatedAt.Time}, AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Token: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time, Dispatched: r.Dispatched, NativeEnabledObserved: r.NativeEnabledObserved}
	if err = json.Unmarshal(r.ObservedSnapshot, &j.ObservedConfiguration); err != nil {
		return j, err
	}
	err = json.Unmarshal(r.DesiredSnapshot, &j.DesiredConfiguration)
	return j, err
}

func saveObjectBucketObjectLock(ctx context.Context, db sqlc.DBTX, j ObjectBucketObjectLock, initial bool) error {
	observed, err := json.Marshal(j.ObservedConfiguration)
	if err != nil {
		return err
	}
	desired, err := json.Marshal(j.DesiredConfiguration)
	if err != nil {
		return err
	}
	var lease pgtype.Timestamptz
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	q := sqlc.New()
	if initial {
		return mapErr(q.ObjectBucketObjectLockInsert(ctx, db, sqlc.ObjectBucketObjectLockInsertParams{BucketID: mustPgUUID(j.BucketID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID), State: j.State, Revision: j.Revision, EnabledRequired: j.EnabledRequired, NativeEnabledObserved: j.NativeEnabledObserved, ObservedKnown: j.ObservedKnown, ObservedSnapshot: observed, DesiredSnapshot: desired, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), Dispatched: j.Dispatched, LastErrorCode: j.LastErrorCode, UpdatedAt: objectUsageTime(j.UpdatedAt)}))
	}
	n, err := q.ObjectBucketObjectLockUpdate(ctx, db, sqlc.ObjectBucketObjectLockUpdateParams{BucketID: mustPgUUID(j.BucketID), State: j.State, Revision: j.Revision, EnabledRequired: j.EnabledRequired, NativeEnabledObserved: j.NativeEnabledObserved, ObservedKnown: j.ObservedKnown, ObservedSnapshot: observed, DesiredSnapshot: desired, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), Dispatched: j.Dispatched, LastErrorCode: j.LastErrorCode, UpdatedAt: objectUsageTime(j.UpdatedAt)})
	if err == nil && n != 1 {
		return ErrConflict
	}
	return mapErr(err)
}

type objectLockMutation func(pgx.Tx, ObjectBucketObjectLock, ObjectBucketVersioning, time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error)

// Follow admission's account-before-bucket lock order. The bucket serializes
// policy and versioning journals, including concurrent first enrollment.
func (s *PgStore) mutateObjectBucketObjectLock(ctx context.Context, account, app, bucket string, fn objectLockMutation) (ObjectBucketObjectLock, error) {
	if account == "" {
		j, err := readObjectBucketObjectLock(ctx, s.pool, bucket)
		if err != nil {
			return j, err
		}
		account, app = j.AccountID, j.AppID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketObjectLock{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return ObjectBucketObjectLock{}, mapErr(err)
	}
	b, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return ObjectBucketObjectLock{}, mapErr(err)
	}
	now, err := q.ObjectVersioningNow(ctx, tx)
	if err != nil {
		return ObjectBucketObjectLock{}, err
	}
	j, err := readObjectBucketObjectLock(ctx, tx, bucket)
	initial := errors.Is(err, ErrNotFound)
	if initial {
		j = newObjectBucketObjectLock(objectBucketFromSQL(b), now.Time)
	} else if err != nil {
		return j, err
	}
	v, err := readObjectVersioning(ctx, tx, bucket)
	if errors.Is(err, ErrNotFound) {
		v = newObjectVersioning(objectBucketFromSQL(b), now.Time)
	} else if err != nil {
		return j, err
	}
	j, v, saveVersioning, err := fn(tx, j, v, now.Time)
	if err != nil {
		return j, err
	}
	// The permanent native-enabled latch must exist before the versioning guard
	// can authorize superseding a previously dispatched suspension.
	if err = saveObjectBucketObjectLock(ctx, tx, j, initial); err != nil {
		return j, err
	}
	if saveVersioning {
		if err = saveObjectVersioning(ctx, tx, v); err != nil {
			return j, err
		}
	}
	return cloneObjectBucketObjectLock(j), mapErr(tx.Commit(ctx))
}

func (s *PgStore) GetObjectBucketObjectLock(ctx context.Context, account, app, bucket string) (ObjectBucketObjectLock, error) {
	b, err := s.GetObjectBucket(ctx, account, app, bucket)
	if err != nil {
		return ObjectBucketObjectLock{}, err
	}
	if b.State == "deleted" {
		return ObjectBucketObjectLock{}, ErrNotFound
	}
	j, err := readObjectBucketObjectLock(ctx, s.pool, bucket)
	if errors.Is(err, ErrNotFound) {
		return newObjectBucketObjectLock(b, time.Now().UTC()), nil
	}
	return j, err
}

func (s *PgStore) RequestObjectBucketObjectLock(ctx context.Context, account, app, bucket string, c api.ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error) {
	return s.mutateObjectBucketObjectLock(ctx, account, app, bucket, func(tx pgx.Tx, j ObjectBucketObjectLock, v ObjectBucketVersioning, now time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error) {
		if j.DesiredConfiguration != nil && j.DesiredConfiguration.Equal(c) {
			return j, v, false, nil
		}
		r, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, v, false, err
		}
		if r.Unsafe {
			return j, v, false, ErrConflict
		}
		deleting, err := sqlc.New().ObjectDeletionActive(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, v, false, err
		}
		capacity, err := activeObjectLockCapacity(ctx, tx, bucket)
		if err != nil {
			return j, v, false, err
		}
		if deleting || capacity {
			return j, v, false, ErrConflict
		}
		j, err = requestObjectBucketObjectLock(j, c, now)
		if err == nil {
			v, err = requireObjectLockVersioning(v, j, now)
		}
		return j, v, true, err
	})
}

func (s *PgStore) ObserveObjectBucketObjectLock(ctx context.Context, account, app, bucket string, revision int64, c api.ObjectBucketObjectLockConfiguration, known bool) (ObjectBucketObjectLock, error) {
	return s.mutateObjectBucketObjectLock(ctx, account, app, bucket, func(tx pgx.Tx, j ObjectBucketObjectLock, v ObjectBucketVersioning, now time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error) {
		j, err := observeObjectBucketObjectLock(j, revision, c, known, objectLockVersioningReady(v), now)
		if err != nil {
			return j, v, false, err
		}
		deleting, err := sqlc.New().ObjectDeletionActive(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, v, false, err
		}
		if j.EnabledRequired && !deleting {
			v, err = requireObjectLockVersioning(v, j, now)
		}
		return j, v, j.EnabledRequired && !deleting, err
	})
}

func (s *PgStore) DueObjectBucketObjectLock(ctx context.Context, limit int32) ([]ObjectBucketObjectLock, error) {
	if limit < 1 || limit > api.ObjectBucketObjectLockBatch {
		return nil, ErrConflict
	}
	ids, err := sqlc.New().ObjectBucketObjectLockDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectBucketObjectLock, 0, len(ids))
	for _, id := range ids {
		j, e := readObjectBucketObjectLock(ctx, s.pool, pgUUIDString(id))
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, nil
}

func activeObjectLockCapacity(ctx context.Context, db sqlc.DBTX, bucket string) (bool, error) {
	_, err := sqlc.New().ObjectCapacityActive(ctx, db, mustPgUUID(bucket))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func objectLockDrainReady(ctx context.Context, db sqlc.DBTX, bucket string) (bool, error) {
	r, err := sqlc.New().ObjectCapacityReadiness(ctx, db, mustPgUUID(bucket))
	if err != nil {
		return false, err
	}
	capacity, err := activeObjectLockCapacity(ctx, db, bucket)
	return r.Pending == 0 && !r.Unsafe && !r.Multipart && !capacity, err
}

func (s *PgStore) ClaimObjectBucketObjectLock(ctx context.Context, bucket, token string) (ObjectBucketObjectLock, error) {
	return s.mutateObjectBucketObjectLock(ctx, "", "", bucket, func(tx pgx.Tx, j ObjectBucketObjectLock, v ObjectBucketVersioning, now time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error) {
		r, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, v, false, err
		}
		capacity, err := activeObjectLockCapacity(ctx, tx, bucket)
		if err != nil {
			return j, v, false, err
		}
		j, err = claimObjectBucketObjectLock(j, token, objectLockVersioningReady(v), r.Pending, r.Unsafe, r.Multipart, capacity, now)
		return j, v, false, err
	})
}

func (s *PgStore) DispatchObjectBucketObjectLock(ctx context.Context, bucket, token string) (ObjectBucketObjectLock, error) {
	return s.mutateObjectBucketObjectLock(ctx, "", "", bucket, func(tx pgx.Tx, j ObjectBucketObjectLock, v ObjectBucketVersioning, now time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error) {
		drained, err := objectLockDrainReady(ctx, tx, bucket)
		if err != nil {
			return j, v, false, err
		}
		if !validObjectLockLease(j, token, now) || j.DesiredConfiguration == nil || !objectLockVersioningReady(v) || !drained {
			return j, v, false, ErrConflict
		}
		j.Dispatched, j.UpdatedAt = true, now
		return j, v, false, nil
	})
}

func (s *PgStore) FinishObjectBucketObjectLock(ctx context.Context, bucket, token string, c api.ObjectBucketObjectLockConfiguration) (ObjectBucketObjectLock, error) {
	return s.mutateObjectBucketObjectLock(ctx, "", "", bucket, func(tx pgx.Tx, j ObjectBucketObjectLock, v ObjectBucketVersioning, now time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error) {
		drained, err := objectLockDrainReady(ctx, tx, bucket)
		if err != nil {
			return j, v, false, err
		}
		if objectLockNeedsDrain(j) && !drained {
			return j, v, false, ErrConflict
		}
		j, err = finishObjectBucketObjectLock(j, token, c, objectLockVersioningReady(v), now)
		return j, v, false, err
	})
}

func (s *PgStore) RetryObjectBucketObjectLock(ctx context.Context, bucket, token, code string) error {
	_, err := s.mutateObjectBucketObjectLock(ctx, "", "", bucket, func(_ pgx.Tx, j ObjectBucketObjectLock, v ObjectBucketVersioning, now time.Time) (ObjectBucketObjectLock, ObjectBucketVersioning, bool, error) {
		j, err := retryObjectBucketObjectLock(j, token, code, now)
		return j, v, false, err
	})
	return err
}
