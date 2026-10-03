package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectDeletionStore = (*PgStore)(nil)
var _ ObjectDeletionActivityStore = (*PgStore)(nil)

func (s *PgStore) HasActiveObjectDeletion(ctx context.Context, account, app, bucket string) (bool, error) {
	if _, err := s.GetObjectBucket(ctx, account, app, bucket); err != nil {
		return false, err
	}
	return sqlc.New().ObjectDeletionActive(ctx, s.pool, mustPgUUID(bucket))
}

func readDeletion(ctx context.Context, db sqlc.DBTX, id string) (ObjectDeletion, error) {
	if _, e := uuid.Parse(id); e != nil {
		return ObjectDeletion{}, ErrNotFound
	}
	r, e := sqlc.New().ObjectDeletionGet(ctx, db, mustPgUUID(id))
	if e != nil {
		return ObjectDeletion{}, mapErr(e)
	}
	j := ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: id, BucketID: pgUUIDString(r.BucketID), Key: r.ObjectKey, Selector: r.Selector, State: r.State, VersionID: r.VersionID, DeleteMarker: r.DeleteMarker, LastErrorCode: r.LastErrorCode, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time}, AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), Token: r.LeaseToken, LeaseUntil: r.LeaseUntil.Time, RetryAt: r.RetryAt.Time, ProviderStatus: r.ProviderStatus, ProviderVersionID: r.ProviderVersionID, ReservedBytes: r.ReservedBytes}
	j.TargetProviderVersionID = r.TargetProviderVersionID
	j.RecoveryClaimed = r.RecoveryClaimed
	if r.LifecycleScanID.Valid {
		var binding ObjectLifecycleDeletionBinding
		if e = json.Unmarshal(r.LifecycleBinding, &binding); e != nil || !validLifecycleDeletionBinding(&binding, j.Selector) || binding.ScanID != pgUUIDString(r.LifecycleScanID) {
			return ObjectDeletion{}, ErrConflict
		}
		j.Lifecycle = &binding
	}
	if e = json.Unmarshal(r.Baseline, &j.Baseline); e != nil || !validDeletionBaseline(j.Baseline) {
		return ObjectDeletion{}, ErrConflict
	}
	return j, nil
}
func saveDeletion(ctx context.Context, db sqlc.DBTX, j ObjectDeletion) error {
	baseline, e := json.Marshal(append([]string{}, j.Baseline...))
	if e != nil {
		return e
	}
	lease := pgtype.Timestamptz{}
	if !j.LeaseUntil.IsZero() {
		lease = objectUsageTime(j.LeaseUntil)
	}
	return mapErr(sqlc.New().ObjectDeletionSave(ctx, db, sqlc.ObjectDeletionSaveParams{ID: mustPgUUID(j.ID), State: j.State, Baseline: baseline, ProviderVersionID: j.ProviderVersionID, VersionID: j.VersionID, DeleteMarker: j.DeleteMarker, LeaseToken: j.Token, LeaseUntil: lease, RetryAt: objectUsageTime(j.RetryAt), LastErrorCode: j.LastErrorCode, UpdatedAt: objectUsageTime(j.UpdatedAt), RecoveryClaimed: j.RecoveryClaimed}))
}
func (s *PgStore) BeginObjectDeletion(ctx context.Context, j ObjectDeletion, policy api.ObjectStoragePolicy) (ObjectDeletion, bool, error) {
	if !validDeletionIdentity(j) {
		return ObjectDeletion{}, false, ErrConflict
	}
	j = newDeletionIntent(j)
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return ObjectDeletion{}, false, e
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, e = q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(j.BucketID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID)}); e != nil {
		return ObjectDeletion{}, false, mapErr(e)
	}
	if _, e = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(j.AccountID)); e != nil {
		return ObjectDeletion{}, false, mapErr(e)
	}
	old, e := readDeletion(ctx, tx, j.ID)
	if e == nil {
		if old.AccountID != j.AccountID || old.BucketID != j.BucketID {
			return ObjectDeletion{}, false, ErrNotFound
		}
		if old.Key != j.Key || old.Selector != j.Selector || !sameLifecycleDeletionBinding(old.Lifecycle, j.Lifecycle) {
			return old, false, ErrConflict
		}
		return old, false, tx.Commit(ctx)
	}
	if !errors.Is(e, ErrNotFound) {
		return old, false, e
	}
	if immutableDeletion(j) {
		j.TargetProviderVersionID, e = q.ObjectVersionReferenceResolve(ctx, tx, sqlc.ObjectVersionReferenceResolveParams{ID: mustPgUUID(j.Selector), AccountID: mustPgUUID(j.AccountID), BucketID: mustPgUUID(j.BucketID), ObjectKey: j.Key})
		if e != nil {
			return j, false, mapErr(e)
		}
	}
	if e = validateLifecycleDeletionTx(ctx, tx, j); e != nil {
		return ObjectDeletion{}, false, e
	}
	fenced, e := q.ObjectCapacityFenced(ctx, tx, mustPgUUID(j.BucketID))
	if e != nil {
		return j, false, e
	}
	ready, e := q.ObjectCapacityReadiness(ctx, tx, mustPgUUID(j.BucketID))
	if e != nil {
		return j, false, e
	}
	v, e := readObjectVersioning(ctx, tx, j.BucketID)
	if e != nil && !errors.Is(e, ErrNotFound) {
		return j, false, e
	}
	if !immutableDeletion(j) {
		j.ProviderStatus = v.ObservedStatus
	}
	if fenced || ready.Pending > 0 || ready.Multipart || !immutableDeletion(j) && ready.Unsafe && (ready.Versions.Bool || j.ProviderStatus != "") {
		return ObjectDeletion{}, false, ErrConflict
	}
	now, e := q.ObjectVersioningNow(ctx, tx)
	if e != nil {
		return j, false, e
	}
	snapshot, e := readObjectUsage(ctx, tx, j.AccountID, now.Time)
	if e != nil {
		return j, false, e
	}
	if !immutableDeletion(j) && ready.Versions.Bool && (!versionAdmissionMode(snapshot, j.BucketID) || j.ProviderStatus == "") {
		return ObjectDeletion{}, false, ErrConflict
	}
	if j.Selector == "" && j.ProviderStatus != "" {
		j.ReservedBytes = int64(len(j.Key))
		if _, _, e = checkObjectAdmission(snapshot, j.BucketID, j.ReservedBytes, 0, false, true, policy, now.Time); e != nil {
			return j, false, e
		}
	}
	if e = q.ObjectVersioningEnsureUsage(ctx, tx, mustPgUUID(j.BucketID)); e != nil {
		return j, false, mapErr(e)
	}
	if j.ReservedBytes > 0 {
		if e = q.ObjectUsageGrantIncrement(ctx, tx, sqlc.ObjectUsageGrantIncrementParams{BucketID: mustPgUUID(j.BucketID), GrantedBytes: j.ReservedBytes, GrantedKeys: 1}); e != nil {
			return j, false, mapErr(e)
		}
	}
	j.State = "prepared"
	j.CreatedAt = now.Time
	j.UpdatedAt = now.Time
	j.RetryAt = now.Time
	j.LeaseUntil = now.Time.Add(api.ObjectDeletionLease)
	var lifecycleID pgtype.UUID
	bound := []byte(`{}`)
	if j.Lifecycle != nil {
		lifecycleID = mustPgUUID(j.Lifecycle.ScanID)
		bound, e = json.Marshal(j.Lifecycle)
		if e != nil {
			return j, false, e
		}
	}
	e = q.ObjectDeletionInsert(ctx, tx, sqlc.ObjectDeletionInsertParams{ID: mustPgUUID(j.ID), BucketID: mustPgUUID(j.BucketID), ObjectKey: j.Key, Selector: j.Selector, TargetProviderVersionID: j.TargetProviderVersionID, ProviderStatus: j.ProviderStatus, ReservedBytes: j.ReservedBytes, LeaseToken: j.Token, LeaseUntil: objectUsageTime(j.LeaseUntil), RetryAt: objectUsageTime(now.Time), LifecycleScanID: lifecycleID, LifecycleBinding: bound})
	if e != nil {
		return j, false, mapErr(e)
	}
	return j, true, tx.Commit(ctx)
}
func (s *PgStore) GetObjectDeletion(ctx context.Context, account, bucket, id string) (ObjectDeletion, error) {
	j, e := readDeletion(ctx, s.pool, id)
	if e != nil {
		return j, e
	}
	if j.AccountID != account || j.BucketID != bucket {
		return ObjectDeletion{}, ErrNotFound
	}
	return j, nil
}
func (s *PgStore) mutateDeletion(ctx context.Context, id string, fn func(pgx.Tx, ObjectDeletion, time.Time) (ObjectDeletion, error)) (ObjectDeletion, error) {
	j, e := readDeletion(ctx, s.pool, id)
	if e != nil {
		return j, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return j, e
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if j.Lifecycle != nil {
		if _, e = q.ObjectCapacityLockBucket(ctx, tx, sqlc.ObjectCapacityLockBucketParams{ID: mustPgUUID(j.BucketID), AccountID: mustPgUUID(j.AccountID), AppID: mustPgUUID(j.AppID)}); e != nil {
			return j, mapErr(e)
		}
	}
	if _, e = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(j.AccountID)); e != nil {
		return j, mapErr(e)
	}
	j, e = readDeletion(ctx, tx, id)
	if e != nil {
		return j, e
	}
	now, e := q.ObjectVersioningNow(ctx, tx)
	if e != nil {
		return j, e
	}
	j, e = fn(tx, j, now.Time)
	if e != nil {
		return j, e
	}
	if e = saveDeletion(ctx, tx, j); e != nil {
		return j, e
	}
	return j, tx.Commit(ctx)
}
func (s *PgStore) DispatchObjectDeletion(ctx context.Context, id, token, status string, baseline []string) (ObjectDeletion, error) {
	return s.mutateDeletion(ctx, id, func(tx pgx.Tx, j ObjectDeletion, now time.Time) (ObjectDeletion, error) {
		if err := validateLifecycleDeletionTx(ctx, tx, j); err != nil {
			return j, err
		}
		if j.ProviderStatus != status {
			return j, ErrConflict
		}
		return dispatchDeletion(j, token, status, baseline, now)
	})
}

func validateLifecycleDeletionTx(ctx context.Context, db sqlc.DBTX, j ObjectDeletion) error {
	if j.Lifecycle == nil {
		return nil
	}
	scan, err := readLifecycleScan(ctx, db, j.Lifecycle.ScanID)
	if err != nil {
		return ErrConflict
	}
	policy, err := readLifecyclePolicy(ctx, db, j.BucketID)
	if err != nil {
		return ErrConflict
	}
	now, err := sqlc.New().ObjectVersioningNow(ctx, db)
	if err != nil {
		return err
	}
	return validateLifecycleDeletion(j, scan, policy, now.Time)
}
func (s *PgStore) FinishObjectDeletion(ctx context.Context, result ObjectDeletion) (ObjectDeletion, error) {
	return s.mutateDeletion(ctx, result.ID, func(tx pgx.Tx, j ObjectDeletion, now time.Time) (ObjectDeletion, error) {
		if !validDeletionLease(j, result.Token, now) {
			return j, ErrConflict
		}
		if result.ProviderVersionID != "" {
			items := []ObjectVersionIdentity{{Key: j.Key, ProviderVersionID: result.ProviderVersionID, DeleteMarker: result.DeleteMarker}}
			if !validVersionReferences(items) {
				return j, ErrConflict
			}
			refs, e := recordObjectVersionsTx(ctx, tx, j.BucketID, items)
			if e != nil {
				return j, e
			}
			result.VersionID = refs[0].ID
		}
		out, e := finishDeletion(j, result, now)
		if e != nil {
			return j, e
		}
		if out.State == "failed" && j.ReservedBytes > 0 {
			e = sqlc.New().ObjectUsageGrantIncrement(ctx, tx, sqlc.ObjectUsageGrantIncrementParams{BucketID: mustPgUUID(j.BucketID), GrantedBytes: -j.ReservedBytes, GrantedKeys: -1})
		}
		if e == nil && out.State == "completed" {
			typ, data := deletionEvent(out)
			e = publishObjectEventTx(ctx, tx, out.AccountID, "delete:"+out.ID, typ, data, now)
		}
		return out, mapErr(e)
	})
}
func (s *PgStore) DueObjectDeletions(ctx context.Context, limit int32) ([]ObjectDeletion, error) {
	if limit < 1 || limit > api.ObjectDeletionBatch {
		return nil, ErrConflict
	}
	ids, e := sqlc.New().ObjectDeletionDue(ctx, s.pool, limit)
	if e != nil {
		return nil, e
	}
	out := []ObjectDeletion{}
	for _, id := range ids {
		j, e := readDeletion(ctx, s.pool, pgUUIDString(id))
		if e != nil {
			return nil, e
		}
		out = append(out, j)
	}
	return out, nil
}
func (s *PgStore) ClaimObjectDeletion(ctx context.Context, id, token string) (ObjectDeletion, error) {
	return s.mutateDeletion(ctx, id, func(_ pgx.Tx, j ObjectDeletion, now time.Time) (ObjectDeletion, error) {
		if !deletionActive(j) || token == "" || len(token) > 128 || j.LeaseUntil.After(now) || j.RetryAt.After(now) {
			return j, ErrConflict
		}
		j.Token = token
		j.RecoveryClaimed = j.RecoveryClaimed || j.State == "dispatched"
		j.LeaseUntil = now.Add(api.ObjectDeletionLease)
		j.UpdatedAt = now
		return j, nil
	})
}
func (s *PgStore) RetryObjectDeletion(ctx context.Context, id, token, code string) error {
	if code != "provider_uncertain" && code != "configuration" {
		return ErrConflict
	}
	_, e := s.mutateDeletion(ctx, id, func(_ pgx.Tx, j ObjectDeletion, now time.Time) (ObjectDeletion, error) {
		if !validDeletionLease(j, token, now) {
			return j, ErrConflict
		}
		j.Token = ""
		j.LeaseUntil = time.Time{}
		j.RetryAt = now.Add(api.ObjectDeletionRetry)
		j.UpdatedAt = now
		j.LastErrorCode = code
		return j, nil
	})
	return e
}
