package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectBucketEncryptionStore = (*PgStore)(nil)

func objectBucketEncryptionFromSQL(r sqlc.ObjectBucketEncryption) (ObjectBucketEncryption, error) {
	j := ObjectBucketEncryption{BucketID: pgUUIDString(r.BucketID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), State: r.State, Revision: r.Revision, Token: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time, Dispatched: r.Dispatched, UpdatedAt: r.UpdatedAt.Time}
	var err error
	j.Encryption, err = encryptionSnapshotFromJSON(r.EncryptionSnapshot, j.AccountID)
	if err != nil {
		return j, err
	}
	j.DesiredEncryption, err = encryptionSnapshotFromJSON(r.DesiredSnapshot, j.AccountID)
	return j, err
}

func readObjectBucketEncryption(ctx context.Context, db sqlc.DBTX, bucket string) (ObjectBucketEncryption, error) {
	r, err := sqlc.New().ObjectBucketEncryptionGet(ctx, db, mustPgUUID(bucket))
	if err != nil {
		return ObjectBucketEncryption{}, mapErr(err)
	}
	return objectBucketEncryptionFromSQL(r)
}

func saveObjectBucketEncryption(ctx context.Context, db sqlc.DBTX, j ObjectBucketEncryption, initial bool) error {
	active, err := encryptionSnapshotJSON(j.Encryption)
	if err != nil {
		return err
	}
	desired, err := encryptionSnapshotJSON(j.DesiredEncryption)
	if err != nil {
		return err
	}
	var lease pgtype.Timestamptz
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	q := sqlc.New()
	if initial {
		return mapErr(q.ObjectBucketEncryptionInsert(ctx, db, sqlc.ObjectBucketEncryptionInsertParams{BucketID: mustPgUUID(j.BucketID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID), State: j.State, Revision: j.Revision, EncryptionSnapshot: active, DesiredSnapshot: desired, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), Dispatched: j.Dispatched, UpdatedAt: objectUsageTime(j.UpdatedAt)}))
	}
	n, err := q.ObjectBucketEncryptionUpdate(ctx, db, sqlc.ObjectBucketEncryptionUpdateParams{BucketID: mustPgUUID(j.BucketID), State: j.State, Revision: j.Revision, EncryptionSnapshot: active, DesiredSnapshot: desired, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), Dispatched: j.Dispatched, UpdatedAt: objectUsageTime(j.UpdatedAt)})
	if err == nil && n != 1 {
		return ErrConflict
	}
	return mapErr(err)
}

// Configuration follows admission's account then bucket/policy lock order.
func (s *PgStore) mutateObjectBucketEncryption(ctx context.Context, account, app, bucket string, fn func(ObjectBucketEncryption, time.Time) (ObjectBucketEncryption, error)) (ObjectBucketEncryption, error) {
	if account == "" {
		j, err := readObjectBucketEncryption(ctx, s.pool, bucket)
		if err != nil {
			return j, err
		}
		account, app = j.AccountID, j.AppID
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectBucketEncryption{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return ObjectBucketEncryption{}, mapErr(err)
	}
	b, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return ObjectBucketEncryption{}, mapErr(err)
	}
	deleting, err := q.ObjectDeletionActive(ctx, tx, mustPgUUID(bucket))
	if err != nil {
		return ObjectBucketEncryption{}, err
	}
	if deleting {
		return ObjectBucketEncryption{}, ErrConflict
	}
	now, err := q.ObjectVersioningNow(ctx, tx)
	if err != nil {
		return ObjectBucketEncryption{}, err
	}
	j, err := readObjectBucketEncryption(ctx, tx, bucket)
	initial := errors.Is(err, ErrNotFound)
	if initial {
		j = newObjectBucketEncryption(objectBucketFromSQL(b), now.Time)
	} else if err != nil {
		return j, err
	}
	j, err = fn(j, now.Time)
	if err != nil {
		return j, err
	}
	if err = saveObjectBucketEncryption(ctx, tx, j, initial); err != nil {
		return j, err
	}
	return j, mapErr(tx.Commit(ctx))
}

func (s *PgStore) GetObjectBucketEncryption(ctx context.Context, account, app, bucket string) (ObjectBucketEncryption, error) {
	b, err := s.GetObjectBucket(ctx, account, app, bucket)
	if err != nil {
		return ObjectBucketEncryption{}, err
	}
	j, err := readObjectBucketEncryption(ctx, s.pool, bucket)
	if errors.Is(err, ErrNotFound) {
		return newObjectBucketEncryption(b, time.Now().UTC()), nil
	}
	return j, err
}

func (s *PgStore) RequestObjectBucketEncryption(ctx context.Context, account, app, bucket string, e ObjectEncryptionSnapshot) (ObjectBucketEncryption, error) {
	return s.mutateObjectBucketEncryption(ctx, account, app, bucket, func(j ObjectBucketEncryption, now time.Time) (ObjectBucketEncryption, error) {
		return requestObjectBucketEncryption(j, e, now)
	})
}

func (s *PgStore) DueObjectBucketEncryption(ctx context.Context, limit int32) ([]ObjectBucketEncryption, error) {
	if limit < 1 || limit > api.ObjectBucketEncryptionBatch {
		return nil, ErrConflict
	}
	ids, err := sqlc.New().ObjectBucketEncryptionDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	jobs := make([]ObjectBucketEncryption, 0, len(ids))
	for _, id := range ids {
		j, e := readObjectBucketEncryption(ctx, s.pool, pgUUIDString(id))
		if e != nil {
			return nil, e
		}
		jobs = append(jobs, j)
	}
	return jobs, nil
}

func (s *PgStore) RefreshObjectBucketEncryption(ctx context.Context, account, app, bucket string, revision int64) (ObjectBucketEncryption, error) {
	return s.mutateObjectBucketEncryption(ctx, account, app, bucket, func(j ObjectBucketEncryption, now time.Time) (ObjectBucketEncryption, error) {
		return refreshObjectBucketEncryption(j, revision, now)
	})
}

func (s *PgStore) ClaimObjectBucketEncryption(ctx context.Context, bucket, token string) (ObjectBucketEncryption, error) {
	return s.mutateObjectBucketEncryption(ctx, "", "", bucket, func(j ObjectBucketEncryption, now time.Time) (ObjectBucketEncryption, error) {
		return claimObjectBucketEncryption(j, token, now)
	})
}

func (s *PgStore) DispatchObjectBucketEncryption(ctx context.Context, bucket, token string) (ObjectBucketEncryption, error) {
	return s.mutateObjectBucketEncryption(ctx, "", "", bucket, func(j ObjectBucketEncryption, now time.Time) (ObjectBucketEncryption, error) {
		if !validObjectBucketEncryptionLease(j, token, now) {
			return j, ErrConflict
		}
		j.Dispatched, j.UpdatedAt = true, now
		return j, nil
	})
}

func (s *PgStore) FinishObjectBucketEncryption(ctx context.Context, bucket, token string, e ObjectEncryptionSnapshot) (ObjectBucketEncryption, error) {
	return s.mutateObjectBucketEncryption(ctx, "", "", bucket, func(j ObjectBucketEncryption, now time.Time) (ObjectBucketEncryption, error) {
		return finishObjectBucketEncryption(j, token, e, now)
	})
}

func (s *PgStore) RetryObjectBucketEncryption(ctx context.Context, bucket, token string) error {
	_, err := s.mutateObjectBucketEncryption(ctx, "", "", bucket, func(j ObjectBucketEncryption, now time.Time) (ObjectBucketEncryption, error) {
		return retryObjectBucketEncryption(j, token, now)
	})
	return err
}

func captureObjectBucketDefaultSQL(ctx context.Context, db sqlc.DBTX, bucket string, e ObjectEncryptionSnapshot) (ObjectEncryptionSnapshot, int64, error) {
	r, err := sqlc.New().ObjectBucketEncryptionForAdmission(ctx, db, mustPgUUID(bucket))
	if errors.Is(err, pgx.ErrNoRows) {
		return e.Clone(), 0, nil
	}
	if err != nil {
		return e, 0, mapErr(err)
	}
	j, err := objectBucketEncryptionFromSQL(r)
	if err != nil {
		return e, 0, err
	}
	return captureObjectBucketDefault(j, e)
}
