package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectLifecycleStore = (*PgStore)(nil)

func readLifecycleRules(raw []byte) ([]api.ObjectLifecycleRule, error) {
	var rules []api.ObjectLifecycleRule
	if err := json.Unmarshal(raw, &rules); err != nil {
		return nil, fmt.Errorf("decode object lifecycle rules: %w", err)
	}
	normal, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil {
		return nil, fmt.Errorf("validate stored object lifecycle rules: %w", err)
	}
	return normal, nil
}

func readLifecyclePolicy(ctx context.Context, db sqlc.DBTX, bucket string) (ObjectLifecyclePolicy, error) {
	r, err := sqlc.New().ObjectLifecyclePolicyGet(ctx, db, mustPgUUID(bucket))
	if err != nil {
		return ObjectLifecyclePolicy{}, mapErr(err)
	}
	rules, err := readLifecycleRules(r.Rules)
	if err != nil {
		return ObjectLifecyclePolicy{}, err
	}
	return ObjectLifecyclePolicy{ObjectBucketLifecycle: api.ObjectBucketLifecycle{BucketID: bucket, Revision: r.Revision, Rules: rules, UpdatedAt: r.UpdatedAt.Time}, AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), NextScanAt: r.NextScanAt.Time}, nil
}

func saveLifecyclePolicy(ctx context.Context, db sqlc.DBTX, p ObjectLifecyclePolicy) error {
	raw, err := json.Marshal(p.Rules)
	if err != nil {
		return fmt.Errorf("encode object lifecycle rules: %w", err)
	}
	return mapErr(sqlc.New().ObjectLifecyclePolicySave(ctx, db, sqlc.ObjectLifecyclePolicySaveParams{BucketID: mustPgUUID(p.BucketID), Revision: p.Revision, Rules: raw, NextScanAt: objectUsageTime(p.NextScanAt), UpdatedAt: objectUsageTime(p.UpdatedAt)}))
}

func readLifecycleScan(ctx context.Context, db sqlc.DBTX, id string) (ObjectLifecycleScan, error) {
	r, err := sqlc.New().ObjectLifecycleScanGet(ctx, db, mustPgUUID(id))
	if err != nil {
		return ObjectLifecycleScan{}, mapErr(err)
	}
	rules, err := readLifecycleRules(r.Rules)
	if err != nil {
		return ObjectLifecycleScan{}, err
	}
	j := ObjectLifecycleScan{ObjectLifecycleScan: api.ObjectLifecycleScan{ID: id, BucketID: pgUUIDString(r.BucketID), Revision: r.Revision, State: r.State, ScannedKeys: r.ScannedKeys, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}, AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Token: r.LeaseToken, LastKey: r.LastKey, Rules: rules, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time}
	if r.FinishedAt.Valid {
		j.FinishedAt = &r.FinishedAt.Time
	}
	return j, nil
}

func activeLifecycleScan(ctx context.Context, db sqlc.DBTX, bucket string) (ObjectLifecycleScan, error) {
	id, err := sqlc.New().ObjectLifecycleScanActive(ctx, db, mustPgUUID(bucket))
	if err != nil {
		return ObjectLifecycleScan{}, mapErr(err)
	}
	return readLifecycleScan(ctx, db, pgUUIDString(id))
}

func saveLifecycleScan(ctx context.Context, db sqlc.DBTX, j ObjectLifecycleScan) error {
	var lease, finished pgtype.Timestamptz
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	if j.FinishedAt != nil {
		finished = objectUsageTime(*j.FinishedAt)
	}
	return mapErr(sqlc.New().ObjectLifecycleScanSave(ctx, db, sqlc.ObjectLifecycleScanSaveParams{ID: mustPgUUID(j.ID), State: j.State, LastKey: j.LastKey, ScannedKeys: j.ScannedKeys, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), UpdatedAt: objectUsageTime(j.UpdatedAt), FinishedAt: finished}))
}

// Bucket then account locks follow the existing storage mutation lock order.
// Read policy and scan again after locking; pre-lock reads only select owners.
func (s *PgStore) withLifecyclePolicy(ctx context.Context, account, app, bucket string, fn func(pgx.Tx, ObjectLifecyclePolicy, time.Time) (ObjectLifecyclePolicy, error)) (ObjectLifecyclePolicy, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectLifecyclePolicy{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	b, err := q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		return ObjectLifecyclePolicy{}, mapErr(err)
	}
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return ObjectLifecyclePolicy{}, mapErr(err)
	}
	now, err := q.ObjectVersioningNow(ctx, tx)
	if err != nil {
		return ObjectLifecyclePolicy{}, err
	}
	p, err := readLifecyclePolicy(ctx, tx, bucket)
	if errors.Is(err, ErrNotFound) {
		p = newLifecyclePolicy(objectBucketFromSQL(b), now.Time)
	} else if err != nil {
		return p, err
	}
	p, err = fn(tx, p, now.Time)
	if err != nil {
		return p, err
	}
	return p, tx.Commit(ctx)
}

func (s *PgStore) GetObjectBucketLifecycle(ctx context.Context, account, app, bucket string) (ObjectLifecyclePolicy, error) {
	b, err := s.GetObjectBucket(ctx, account, app, bucket)
	if err != nil {
		return ObjectLifecyclePolicy{}, err
	}
	if b.State != "ready" {
		return ObjectLifecyclePolicy{}, ErrConflict
	}
	p, err := readLifecyclePolicy(ctx, s.pool, bucket)
	if errors.Is(err, ErrNotFound) {
		return newLifecyclePolicy(b, time.Now().UTC()), nil
	}
	return p, err
}

func (s *PgStore) SetObjectBucketLifecycle(ctx context.Context, account, app, bucket string, rules []api.ObjectLifecycleRule) (ObjectLifecyclePolicy, error) {
	normal, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil {
		return ObjectLifecyclePolicy{}, ErrConflict
	}
	return s.withLifecyclePolicy(ctx, account, app, bucket, func(tx pgx.Tx, p ObjectLifecyclePolicy, now time.Time) (ObjectLifecyclePolicy, error) {
		if lifecycleRulesEqual(p.Rules, normal) {
			return p, nil
		}
		if p.Revision >= api.MaxObjectStoragePolicyValue {
			return p, ErrConflict
		}
		old, err := activeLifecycleScan(ctx, tx, bucket)
		if err == nil {
			if old.LeaseUntil.After(now) {
				return p, ErrConflict
			}
			old.State, old.Token, old.LeaseUntil, old.UpdatedAt, old.FinishedAt = "cancelled", "", time.Time{}, now, &now
			if err = saveLifecycleScan(ctx, tx, old); err != nil {
				return p, err
			}
		} else if !errors.Is(err, ErrNotFound) {
			return p, err
		}
		p.Revision++
		p.Rules, p.NextScanAt, p.UpdatedAt = normal, now, now
		return p, saveLifecyclePolicy(ctx, tx, p)
	})
}

func (s *PgStore) DueObjectLifecyclePolicies(ctx context.Context, limit int32) ([]ObjectLifecyclePolicy, error) {
	if limit < 1 || limit > api.ObjectLifecycleBatch {
		return nil, ErrConflict
	}
	ids, err := sqlc.New().ObjectLifecyclePolicyDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := []ObjectLifecyclePolicy{}
	for _, id := range ids {
		p, err := readLifecyclePolicy(ctx, s.pool, pgUUIDString(id))
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *PgStore) StartObjectLifecycleScan(ctx context.Context, account, app, bucket string) (ObjectLifecycleScan, error) {
	var j ObjectLifecycleScan
	_, err := s.withLifecyclePolicy(ctx, account, app, bucket, func(tx pgx.Tx, p ObjectLifecyclePolicy, now time.Time) (ObjectLifecyclePolicy, error) {
		old, err := activeLifecycleScan(ctx, tx, bucket)
		if err == nil {
			j = old
			return p, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return p, err
		}
		if !lifecycleEnabled(p) || p.NextScanAt.After(now) {
			return p, ErrConflict
		}
		j = newLifecycleScan(p, now)
		raw, err := json.Marshal(j.Rules)
		if err != nil {
			return p, fmt.Errorf("encode lifecycle scan rules: %w", err)
		}
		return p, mapErr(sqlc.New().ObjectLifecycleScanInsert(ctx, tx, sqlc.ObjectLifecycleScanInsertParams{ID: mustPgUUID(j.ID), BucketID: mustPgUUID(bucket), Revision: j.Revision, Rules: raw, RetryAt: objectUsageTime(now)}))
	})
	return j, err
}

func (s *PgStore) GetObjectLifecycleScan(ctx context.Context, account, bucket, id string) (ObjectLifecycleScan, error) {
	j, err := readLifecycleScan(ctx, s.pool, id)
	if err == nil && (j.AccountID != account || j.BucketID != bucket) {
		return ObjectLifecycleScan{}, ErrNotFound
	}
	return j, err
}

func (s *PgStore) mutateLifecycleScan(ctx context.Context, id string, fn func(ObjectLifecycleScan, time.Time) (ObjectLifecycleScan, error)) (ObjectLifecycleScan, error) {
	initial, err := readLifecycleScan(ctx, s.pool, id)
	if err != nil {
		return initial, err
	}
	var j ObjectLifecycleScan
	_, err = s.withLifecyclePolicy(ctx, initial.AccountID, initial.AppID, initial.BucketID, func(tx pgx.Tx, p ObjectLifecyclePolicy, now time.Time) (ObjectLifecyclePolicy, error) {
		var err error
		j, err = readLifecycleScan(ctx, tx, id)
		if err != nil {
			return p, err
		}
		if p.Revision != j.Revision {
			return p, ErrConflict
		}
		j, err = fn(j, now)
		if err != nil {
			return p, err
		}
		if err = saveLifecycleScan(ctx, tx, j); err != nil {
			return p, err
		}
		if j.State == "completed" {
			p.NextScanAt = now.Add(api.ObjectLifecycleSweepInterval)
			return p, saveLifecyclePolicy(ctx, tx, p)
		}
		return p, nil
	})
	return j, err
}

func (s *PgStore) ClaimObjectLifecycleScan(ctx context.Context, id, token string) (ObjectLifecycleScan, error) {
	return s.mutateLifecycleScan(ctx, id, func(j ObjectLifecycleScan, now time.Time) (ObjectLifecycleScan, error) {
		return claimLifecycleScan(j, token, now)
	})
}

func (s *PgStore) CheckpointObjectLifecycleScan(ctx context.Context, id, token, key string, done bool) (ObjectLifecycleScan, error) {
	return s.mutateLifecycleScan(ctx, id, func(j ObjectLifecycleScan, now time.Time) (ObjectLifecycleScan, error) {
		return checkpointLifecycleScan(j, token, key, done, now)
	})
}

func (s *PgStore) RetryObjectLifecycleScan(ctx context.Context, id, token string) error {
	_, err := s.mutateLifecycleScan(ctx, id, func(j ObjectLifecycleScan, now time.Time) (ObjectLifecycleScan, error) {
		if !validLifecycleScanLease(j, token, now) {
			return j, ErrConflict
		}
		j.Token, j.LeaseUntil, j.RetryAt, j.UpdatedAt = "", time.Time{}, now.Add(api.ObjectLifecycleRetry), now
		return j, nil
	})
	return err
}
