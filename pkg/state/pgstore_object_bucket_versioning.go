package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

var _ ObjectBucketVersioningStore = (*PgStore)(nil)

func readObjectVersioning(ctx context.Context, db sqlc.DBTX, bucket string) (ObjectBucketVersioning, error) {
	r, err := sqlc.New().ObjectVersioningGet(ctx, db, mustPgUUID(bucket))
	if err != nil {
		return ObjectBucketVersioning{}, mapErr(err)
	}
	j := ObjectBucketVersioning{ObjectBucketVersioning: api.ObjectBucketVersioning{BucketID: bucket, DesiredStatus: r.DesiredStatus, ObservedStatus: r.ObservedStatus, State: r.State, Revision: r.Revision, VersionsRequired: r.VersionsRequired, CapacityJobID: pgUUIDString(r.CapacityJobID), LastErrorCode: r.LastErrorCode, UpdatedAt: r.UpdatedAt.Time}, AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Token: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time, Dispatched: r.Dispatched}
	if r.PropagationUntil.Valid {
		j.PropagationUntil = &r.PropagationUntil.Time
	}
	return j, nil
}
func saveObjectVersioning(ctx context.Context, db sqlc.DBTX, j ObjectBucketVersioning) error {
	var propagation, lease pgtype.Timestamptz
	var capacity pgtype.UUID
	if j.PropagationUntil != nil {
		propagation = objectUsageTime(*j.PropagationUntil)
	}
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	if j.CapacityJobID != "" {
		capacity = mustPgUUID(j.CapacityJobID)
	}
	q := sqlc.New()
	if err := q.ObjectVersioningEnsureUsage(ctx, db, mustPgUUID(j.BucketID)); err != nil {
		return mapErr(err)
	}
	return mapErr(q.ObjectVersioningSave(ctx, db, sqlc.ObjectVersioningSaveParams{BucketID: mustPgUUID(j.BucketID), DesiredStatus: j.DesiredStatus, ObservedStatus: j.ObservedStatus, State: j.State, Revision: j.Revision, VersionsRequired: j.VersionsRequired, Dispatched: j.Dispatched, PropagationUntil: propagation, CapacityJobID: capacity, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), LastErrorCode: j.LastErrorCode, UpdatedAt: objectUsageTime(j.UpdatedAt)}))
}

// NO KEY UPDATE excludes bucket deletion and multipart reservation without
// conflicting with admission FK KEY SHARE locks. Account locks serialize cutover.
func (s *PgStore) mutateObjectVersioning(ctx context.Context, account, app, bucket string, fn func(pgx.Tx, ObjectBucketVersioning, time.Time) (ObjectBucketVersioning, error)) (ObjectBucketVersioning, error) {
	if account == "" {
		j, err := readObjectVersioning(ctx, s.pool, bucket)
		if err != nil {
			return j, err
		}
		account, app = j.AccountID, j.AppID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketVersioning{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	b, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return ObjectBucketVersioning{}, mapErr(err)
	}
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return ObjectBucketVersioning{}, mapErr(err)
	}
	now, err := q.ObjectVersioningNow(ctx, tx)
	if err != nil {
		return ObjectBucketVersioning{}, err
	}
	j, err := readObjectVersioning(ctx, tx, bucket)
	if errors.Is(err, ErrNotFound) {
		j = newObjectVersioning(objectBucketFromSQL(b), now.Time)
	} else if err != nil {
		return j, err
	}
	j, err = fn(tx, j, now.Time)
	if err != nil {
		return j, err
	}
	if err = saveObjectVersioning(ctx, tx, j); err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) GetObjectBucketVersioning(ctx context.Context, account, app, bucket string) (ObjectBucketVersioning, error) {
	b, err := s.GetObjectBucket(ctx, account, app, bucket)
	if err != nil {
		return ObjectBucketVersioning{}, err
	}
	if b.State != "ready" {
		return ObjectBucketVersioning{}, ErrConflict
	}
	j, err := readObjectVersioning(ctx, s.pool, bucket)
	if errors.Is(err, ErrNotFound) {
		return newObjectVersioning(b, time.Now().UTC()), nil
	}
	return j, err
}
func (s *PgStore) RequestObjectBucketVersioning(ctx context.Context, account, app, bucket, status string) (ObjectBucketVersioning, error) {
	return s.mutateObjectVersioning(ctx, account, app, bucket, func(tx pgx.Tx, j ObjectBucketVersioning, now time.Time) (ObjectBucketVersioning, error) {
		ready, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, err
		}
		if ready.Unsafe {
			return j, ErrConflict
		}
		if !versioningActive(j) {
			_, err = sqlc.New().ObjectCapacityActive(ctx, tx, mustPgUUID(bucket))
			if err == nil {
				return j, ErrConflict
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return j, err
			}
		}
		return requestObjectVersioning(j, status, now)
	})
}
func (s *PgStore) ObserveObjectBucketVersioning(ctx context.Context, account, app, bucket, status string) (ObjectBucketVersioning, error) {
	if status != "" && !ValidObjectBucketVersioningStatus(status) {
		return ObjectBucketVersioning{}, ErrConflict
	}
	return s.mutateObjectVersioning(ctx, account, app, bucket, func(_ pgx.Tx, j ObjectBucketVersioning, now time.Time) (ObjectBucketVersioning, error) {
		return observeObjectVersioning(j, status, now), nil
	})
}
func (s *PgStore) DueObjectBucketVersioning(ctx context.Context, limit int32) ([]ObjectBucketVersioning, error) {
	if limit < 1 || limit > api.ObjectBucketVersioningBatch {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectVersioningDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := []ObjectBucketVersioning{}
	for _, id := range rows {
		j, e := readObjectVersioning(ctx, s.pool, pgUUIDString(id))
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *PgStore) ClaimObjectBucketVersioning(ctx context.Context, bucket, token string) (ObjectBucketVersioning, error) {
	return s.mutateObjectVersioning(ctx, "", "", bucket, func(tx pgx.Tx, j ObjectBucketVersioning, now time.Time) (ObjectBucketVersioning, error) {
		if j.State == "inventory" {
			c, err := readObjectCapacityJob(ctx, tx, j.CapacityJobID)
			if err != nil {
				return j, err
			}
			if c.State == "completed" && c.InventoryVerified { /* verify provider after inventory */
			} else if objectCapacityActive(c.State) {
				return j, ErrConflict
			} else {
				j.CapacityJobID = ""
				j.State = "propagating"
				j.LastErrorCode = "inventory_failed"
			}
		}
		r, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, err
		}
		_, err = sqlc.New().ObjectCapacityActive(ctx, tx, mustPgUUID(bucket))
		active := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return j, err
		}
		return claimObjectVersioning(j, token, r.Pending, r.Unsafe, r.Multipart, active, now)
	})
}
func (s *PgStore) DispatchObjectBucketVersioning(ctx context.Context, bucket, token string) (ObjectBucketVersioning, error) {
	return s.mutateObjectVersioning(ctx, "", "", bucket, func(tx pgx.Tx, j ObjectBucketVersioning, now time.Time) (ObjectBucketVersioning, error) {
		if !validVersioningLease(j, token, now) || j.State == "inventory" {
			return j, ErrConflict
		}
		r, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(bucket))
		if err != nil {
			return j, err
		}
		if r.Pending > 0 || r.Unsafe || r.Multipart {
			return j, ErrConflict
		}
		_, err = sqlc.New().ObjectCapacityActive(ctx, tx, mustPgUUID(bucket))
		if err == nil {
			return j, ErrConflict
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return j, err
		}
		j.Dispatched = true
		j.VersionsRequired = true
		t := now.Add(api.ObjectBucketVersioningPropagation)
		j.PropagationUntil = &t
		j.UpdatedAt = now
		return j, nil
	})
}
func (s *PgStore) AdvanceObjectBucketVersioning(ctx context.Context, bucket, token, status string) (ObjectBucketVersioning, error) {
	if status != "" && !ValidObjectBucketVersioningStatus(status) {
		return ObjectBucketVersioning{}, ErrConflict
	}
	return s.mutateObjectVersioning(ctx, "", "", bucket, func(tx pgx.Tx, j ObjectBucketVersioning, now time.Time) (ObjectBucketVersioning, error) {
		if !validVersioningLease(j, token, now) {
			return j, ErrConflict
		}
		done := false
		if j.CapacityJobID != "" {
			c, err := readObjectCapacityJob(ctx, tx, j.CapacityJobID)
			if err != nil {
				return j, err
			}
			done = c.State == "completed" && c.InventoryVerified && c.InventoryScope == ObjectInventoryAllVersions
		}
		j, inventory := advanceObjectVersioning(j, status, done, now)
		if inventory {
			r, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(bucket))
			if err != nil {
				return j, err
			}
			if r.Pending > 0 || r.Unsafe || r.Multipart {
				return j, ErrConflict
			}
			_, err = sqlc.New().ObjectCapacityActive(ctx, tx, mustPgUUID(bucket))
			if err == nil {
				return j, ErrConflict
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return j, err
			}
			snap, err := readObjectUsage(ctx, tx, j.AccountID, now)
			if err != nil {
				return j, err
			}
			bytes, keys := objectCapacityTotals(snap, bucket)
			c, err := sqlc.New().ObjectCapacityInsert(ctx, tx, sqlc.ObjectCapacityInsertParams{ID: mustPgUUID(uuid.NewString()), BucketID: mustPgUUID(bucket), BeforeBytes: bytes, BeforeKeys: keys, DeadlineSeconds: int32(api.ObjectCapacityReconciliationTimeout / time.Second)})
			if err != nil {
				return j, mapErr(err)
			}
			job, err := readObjectCapacityJob(ctx, tx, pgUUIDString(c.ID))
			if err != nil {
				return j, err
			}
			job.InventoryScope = ObjectInventoryAllVersions
			if err = saveObjectCapacityJob(ctx, tx, job); err != nil {
				return j, err
			}
			j.CapacityJobID = job.ID
		}
		return j, nil
	})
}
func (s *PgStore) RetryObjectBucketVersioning(ctx context.Context, bucket, token string) error {
	_, err := s.mutateObjectVersioning(ctx, "", "", bucket, func(_ pgx.Tx, j ObjectBucketVersioning, now time.Time) (ObjectBucketVersioning, error) {
		if !validVersioningLease(j, token, now) {
			return j, ErrConflict
		}
		j = releaseObjectVersioning(j, now)
		j.LastErrorCode = "provider_failed"
		return j, nil
	})
	return err
}
func objectVersioningInventoryAllowed(ctx context.Context, db sqlc.DBTX, bucket, id string) (bool, error) {
	j, err := readObjectVersioning(ctx, db, bucket)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !versioningActive(j) || j.State == "inventory" && j.CapacityJobID == id, nil
}
