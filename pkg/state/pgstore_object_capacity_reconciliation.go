package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectCapacityStore = (*PgStore)(nil)

func (s *PgStore) BeginObjectWrite(ctx context.Context, account, bucket, token, key string, size int64, p api.ObjectStoragePolicy) error {
	if _, err := uuid.Parse(token); err != nil {
		return ErrConflict
	}
	return s.admitObjectURL(ctx, account, bucket, key, size, true, p, token)
}
func (s *PgStore) SettleObjectWrite(ctx context.Context, account, bucket, token string) error {
	if _, err := uuid.Parse(token); err != nil {
		return ErrNotFound
	}
	n, err := sqlc.New().ObjectWriteSettle(ctx, s.pool, sqlc.ObjectWriteSettleParams{ID: mustPgUUID(token), BucketID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func objectCapacityFromSQL(r sqlc.ObjectCapacityGetRow) ObjectCapacityReconciliation {
	j := ObjectCapacityReconciliation{ObjectCapacityReconciliation: api.ObjectCapacityReconciliation{ID: pgUUIDString(r.ID), BucketID: pgUUIDString(r.BucketID), State: r.State, BeforeBytes: r.BeforeBytes, BeforeKeys: r.BeforeKeys, AfterBytes: r.AfterBytes, AfterKeys: r.AfterKeys, ReclaimedBytes: r.ReclaimedBytes, ReclaimedKeys: r.ReclaimedKeys, PendingWrites: r.PendingWrites, LastErrorCode: r.LastErrorCode, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}, AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Token: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time, DeadlineAt: r.DeadlineAt.Time}
	if r.FinishedAt.Valid {
		j.FinishedAt = &r.FinishedAt.Time
	}
	return j
}
func readObjectCapacityJob(ctx context.Context, db sqlc.DBTX, id string) (ObjectCapacityReconciliation, error) {
	if _, err := uuid.Parse(id); err != nil {
		return ObjectCapacityReconciliation{}, ErrNotFound
	}
	r, err := sqlc.New().ObjectCapacityGet(ctx, db, mustPgUUID(id))
	if err != nil {
		return ObjectCapacityReconciliation{}, mapErr(err)
	}
	return objectCapacityFromSQL(r), nil
}
func saveObjectCapacityJob(ctx context.Context, db sqlc.DBTX, j ObjectCapacityReconciliation) error {
	finished := pgtype.Timestamptz{}
	if j.FinishedAt != nil {
		finished = objectUsageTime(*j.FinishedAt)
	}
	lease := pgtype.Timestamptz{}
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	return sqlc.New().ObjectCapacitySave(ctx, db, sqlc.ObjectCapacitySaveParams{ID: mustPgUUID(j.ID), State: j.State, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), BeforeBytes: j.BeforeBytes, BeforeKeys: j.BeforeKeys, AfterBytes: j.AfterBytes, AfterKeys: j.AfterKeys, ReclaimedBytes: j.ReclaimedBytes, ReclaimedKeys: j.ReclaimedKeys, PendingWrites: j.PendingWrites, LastErrorCode: j.LastErrorCode, UpdatedAt: objectUsageTime(j.UpdatedAt), FinishedAt: finished})
}
func (s *PgStore) RequestObjectCapacityReconciliation(ctx context.Context, account, app, bucket string) (ObjectCapacityReconciliation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectCapacityReconciliation{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	// Exclude multipart reservation and bucket deletion without blocking FK
	// KEY SHARE locks held by an account-locked write admission.
	if _, err = q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)}); err != nil {
		return ObjectCapacityReconciliation{}, mapErr(err)
	}
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return ObjectCapacityReconciliation{}, mapErr(err)
	}
	active, err := q.ObjectCapacityActive(ctx, tx, mustPgUUID(bucket))
	if err == nil {
		j, e := readObjectCapacityJob(ctx, tx, pgUUIDString(active.ID))
		if e != nil {
			return j, e
		}
		return j, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectCapacityReconciliation{}, err
	}
	count, err := q.ObjectMultipartCount(ctx, tx, mustPgUUID(bucket))
	if err != nil {
		return ObjectCapacityReconciliation{}, err
	}
	if count > 0 {
		return ObjectCapacityReconciliation{}, ErrConflict
	}
	snap, err := readObjectUsage(ctx, tx, account, time.Now())
	if err != nil {
		return ObjectCapacityReconciliation{}, err
	}
	bytes, keys := objectCapacityTotals(snap, bucket)
	row, err := q.ObjectCapacityInsert(ctx, tx, sqlc.ObjectCapacityInsertParams{ID: mustPgUUID(uuid.NewString()), BucketID: mustPgUUID(bucket), BeforeBytes: bytes, BeforeKeys: keys, DeadlineSeconds: int32(api.ObjectCapacityReconciliationTimeout / time.Second)})
	if err != nil {
		return ObjectCapacityReconciliation{}, mapErr(err)
	}
	j, err := readObjectCapacityJob(ctx, tx, pgUUIDString(row.ID))
	if err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) GetObjectCapacityReconciliation(ctx context.Context, account, bucket, id string) (ObjectCapacityReconciliation, error) {
	j, err := readObjectCapacityJob(ctx, s.pool, id)
	if err != nil {
		return ObjectCapacityReconciliation{}, err
	}
	if j.AccountID != account || j.BucketID != bucket {
		return ObjectCapacityReconciliation{}, ErrNotFound
	}
	return j, nil
}

// Account locking serializes every readiness check, admission and quota rebase.
func (s *PgStore) lockObjectCapacityJob(ctx context.Context, id string) (pgx.Tx, ObjectCapacityReconciliation, error) {
	j, err := readObjectCapacityJob(ctx, s.pool, id)
	if err != nil {
		return nil, j, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, j, err
	}
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(j.AccountID)); err == nil {
		_, err = q.ObjectCapacityLock(ctx, tx, mustPgUUID(id))
	}
	if err == nil {
		j, err = readObjectCapacityJob(ctx, tx, id)
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, j, mapErr(err)
	}
	return tx, j, nil
}
func (s *PgStore) CancelObjectCapacityReconciliation(ctx context.Context, account, bucket, id string) (ObjectCapacityReconciliation, error) {
	tx, j, err := s.lockObjectCapacityJob(ctx, id)
	if err != nil {
		return ObjectCapacityReconciliation{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if j.AccountID != account || j.BucketID != bucket {
		return ObjectCapacityReconciliation{}, ErrNotFound
	}
	if objectCapacityActive(j.State) {
		now := time.Now().UTC()
		j.State = "cancelled"
		j.Token = ""
		j.LeaseUntil = time.Time{}
		j.UpdatedAt = now
		j.FinishedAt = &now
		if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
			return j, err
		}
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) DueObjectCapacityReconciliations(ctx context.Context, limit int32) ([]ObjectCapacityReconciliation, error) {
	if limit < 1 || limit > api.ObjectCapacityReconciliationBatch {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectCapacityDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectCapacityReconciliation, 0, len(rows))
	for _, r := range rows {
		j, e := readObjectCapacityJob(ctx, s.pool, pgUUIDString(r.ID))
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *PgStore) ClaimObjectCapacityReconciliation(ctx context.Context, id, token string) (ObjectCapacityReconciliation, error) {
	tx, j, err := s.lockObjectCapacityJob(ctx, id)
	if err != nil {
		return j, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := time.Now().UTC()
	if token == "" || !objectCapacityActive(j.State) || j.LeaseUntil.After(now) || j.RetryAt.After(now) {
		return j, ErrConflict
	}
	ready, err := sqlc.New().ObjectCapacityReadiness(ctx, tx, mustPgUUID(j.BucketID))
	if err != nil {
		return j, err
	}
	snap, err := readObjectUsage(ctx, tx, j.AccountID, now)
	if err != nil {
		return j, err
	}
	j.BeforeBytes, j.BeforeKeys = objectCapacityTotals(snap, j.BucketID)
	j.AfterBytes, j.AfterKeys = j.BeforeBytes, j.BeforeKeys
	j = prepareObjectCapacityClaim(j, token, ready.Pending, ready.Unsafe, ready.Multipart, now)
	if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) FinishObjectCapacityReconciliation(ctx context.Context, id, token string, bytes, keys int64) (ObjectCapacityReconciliation, error) {
	tx, j, err := s.lockObjectCapacityJob(ctx, id)
	if err != nil {
		return j, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := time.Now().UTC()
	if !validObjectCapacityFinish(j, token, bytes, keys, now) {
		return j, ErrConflict
	}
	q := sqlc.New()
	ready, err := q.ObjectCapacityReadiness(ctx, tx, mustPgUUID(j.BucketID))
	if err != nil {
		return j, err
	}
	if ready.Pending > 0 || ready.Unsafe || ready.Multipart {
		return j, ErrConflict
	}
	n, err := q.ObjectCapacityRebase(ctx, tx, sqlc.ObjectCapacityRebaseParams{ID: mustPgUUID(j.BucketID), Bytes: bytes, Keys: keys})
	if err != nil {
		return j, err
	}
	if n != 1 {
		return j, ErrConflict
	}
	if err = q.ObjectCapacityDeleteGrants(ctx, tx, mustPgUUID(j.BucketID)); err != nil {
		return j, err
	}
	if err = q.ObjectCapacityDeleteWrites(ctx, tx, mustPgUUID(j.BucketID)); err != nil {
		return j, err
	}
	if err = q.ObjectInventorySample(ctx, tx, sqlc.ObjectInventorySampleParams{BucketID: mustPgUUID(j.BucketID), Token: token}); err != nil {
		return j, err
	}
	j = completeObjectCapacityJob(j, bytes, keys, now)
	if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
		return j, err
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) RetryObjectCapacityReconciliation(ctx context.Context, id, token string) error {
	tx, j, err := s.lockObjectCapacityJob(ctx, id)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	now := time.Now().UTC()
	if !validObjectCapacityFinish(j, token, 0, 0, now) {
		return ErrConflict
	}
	j.State = "waiting"
	j.Token = ""
	j.LeaseUntil = time.Time{}
	j.UpdatedAt = now
	j.RetryAt = now.Add(api.ObjectCapacityReconciliationRetry)
	j.LastErrorCode = "inventory_failed"
	if err = saveObjectCapacityJob(ctx, tx, j); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
