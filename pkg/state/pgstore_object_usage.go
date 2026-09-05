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

var _ ObjectStorageAccountingStore = (*PgStore)(nil)

func objectUsageTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (s *PgStore) ObjectUsage(ctx context.Context, account string, now time.Time) (ObjectUsageSnapshot, error) {
	return readObjectUsage(ctx, s.pool, account, now)
}

func readObjectUsage(ctx context.Context, db sqlc.DBTX, account string, now time.Time) (ObjectUsageSnapshot, error) {
	q := sqlc.New()
	out := ObjectUsageSnapshot{}
	rows, err := q.ObjectUsageBuckets(ctx, db, mustPgUUID(account))
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Buckets = append(out.Buckets, ObjectBucketUsage{
			Bucket:        ObjectBucket{ID: pgUUIDString(r.ID), AccountID: account, AppID: pgUUIDString(r.AppID), BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, PhysicalName: r.PhysicalName, State: r.State, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
			BaselineBytes: r.BaselineBytes.Int64, BaselineKeys: r.BaselineKeys.Int64, GrantedBytes: r.GrantedBytes.Int64, GrantedKeys: r.GrantedKeys.Int64,
			ObservedBytes: r.ObservedBytes.Int64, ObservedKeys: r.ObservedKeys.Int64, ObservedAt: r.ObservedAt.Time, AttemptAt: r.AttemptAt.Time, LeaseUntil: r.InventoryLeaseUntil.Time, Token: r.Token.String,
		})
	}
	reports, err := q.ObjectUsageReports(ctx, db, sqlc.ObjectUsageReportsParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(ObjectStoragePeriod(now))})
	if err != nil {
		return out, err
	}
	for _, r := range reports {
		out.Reports = append(out.Reports, objectReportFromSQL(r))
	}
	out.Authorizations, err = q.ObjectUsageAuthorizationCount(ctx, db, sqlc.ObjectUsageAuthorizationCountParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(ObjectStoragePeriod(now))})
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return out, err
}

func objectReportFromSQL(r sqlc.ObjectStorageUsageReport) api.ObjectStorageUsageReport {
	return api.ObjectStorageUsageReport{AccountID: pgUUIDString(r.AccountID), BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, Source: r.Source, PeriodStart: r.PeriodStart.Time, ObservedAt: r.ObservedAt.Time, StoredByteHours: r.StoredByteHours, RequestCount: r.RequestCount, EgressBytes: r.EgressBytes, CostMillicents: r.CostMillicents}
}

func (s *PgStore) AdmitObjectURL(ctx context.Context, account, bucket, key string, size int64, put bool, p api.ObjectStoragePolicy) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return mapErr(err)
	}
	now := time.Now().UTC()
	snapshot, err := readObjectUsage(ctx, tx, account, now)
	if err != nil {
		return err
	}
	old, err := q.ObjectUsageGrant(ctx, tx, sqlc.ObjectUsageGrantParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key)})
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	delta, keys, err := checkObjectAdmission(snapshot, bucket, size, old, exists, put, p, now)
	if err != nil {
		return err
	}
	if put {
		if err = q.ObjectUsageGrantUpsert(ctx, tx, sqlc.ObjectUsageGrantUpsertParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), MaxBytes: size}); err != nil {
			return err
		}
		if err = q.ObjectUsageGrantIncrement(ctx, tx, sqlc.ObjectUsageGrantIncrementParams{BucketID: mustPgUUID(bucket), GrantedBytes: delta, GrantedKeys: keys}); err != nil {
			return err
		}
	}
	if err = q.ObjectUsageAuthorize(ctx, tx, sqlc.ObjectUsageAuthorizeParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(ObjectStoragePeriod(now))}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) RecordObjectUsageReport(ctx context.Context, r api.ObjectStorageUsageReport) error {
	r = normalizeObjectReport(r)
	if !validObjectReport(r, time.Now()) {
		return ErrConflict
	}
	id, _ := uuid.Parse(r.AccountID)
	r.AccountID = id.String()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return mapErr(err)
	}
	old, err := q.ObjectUsageReportGet(ctx, tx, sqlc.ObjectUsageReportGetParams{AccountID: mustPgUUID(r.AccountID), BackendID: r.BackendID, PeriodStart: objectUsageTime(r.PeriodStart), ObservedAt: objectUsageTime(r.ObservedAt)})
	if err == nil {
		if sameObjectReport(objectReportFromSQL(old), r) {
			return tx.Commit(ctx)
		}
		return ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	snapshot, err := readObjectUsage(ctx, tx, r.AccountID, r.ObservedAt)
	if err != nil {
		return err
	}
	if err = checkObjectReport(snapshot, r); err != nil {
		return err
	}
	err = q.ObjectUsageReportInsert(ctx, tx, sqlc.ObjectUsageReportInsertParams{AccountID: mustPgUUID(r.AccountID), BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, Source: r.Source, PeriodStart: objectUsageTime(r.PeriodStart), ObservedAt: objectUsageTime(r.ObservedAt), StoredByteHours: r.StoredByteHours, RequestCount: r.RequestCount, EgressBytes: r.EgressBytes, CostMillicents: r.CostMillicents})
	if err != nil {
		return mapErr(err)
	}
	if err = q.ObjectUsageReportHead(ctx, tx, sqlc.ObjectUsageReportHeadParams{AccountID: mustPgUUID(r.AccountID), BackendID: r.BackendID, PeriodStart: objectUsageTime(r.PeriodStart), ObservedAt: objectUsageTime(r.ObservedAt)}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *PgStore) DueObjectInventories(ctx context.Context, limit int32) ([]ObjectBucket, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrConflict
	}
	rows, err := sqlc.New().ObjectInventoriesDue(ctx, s.pool, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectBucket, 0, len(rows))
	for _, r := range rows {
		out = append(out, objectBucketFromSQL(r))
	}
	return out, nil
}

func (s *PgStore) ClaimObjectInventory(ctx context.Context, bucket, token string) error {
	if token == "" {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectInventoryClaim(ctx, s.pool, sqlc.ObjectInventoryClaimParams{ID: mustPgUUID(bucket), Token: token})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) FinishObjectInventory(ctx context.Context, bucket, token string, bytes, objects int64) error {
	if token == "" || bytes < 0 || objects < 0 || bytes > api.MaxObjectStoragePolicyValue || objects > api.MaxObjectStoragePolicyValue {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	account, err := q.ObjectUsageBucketAccount(ctx, tx, mustPgUUID(bucket))
	if err != nil {
		return mapErr(err)
	}
	if _, err = q.ObjectUsageLockAccount(ctx, tx, account); err != nil {
		return mapErr(err)
	}
	// Serialize inventory publication with account-wide quota decisions.
	n, err := q.ObjectInventoryFinish(ctx, tx, sqlc.ObjectInventoryFinishParams{BucketID: mustPgUUID(bucket), Token: token, Bytes: bytes, Objects: objects})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if err = q.ObjectInventorySample(ctx, tx, sqlc.ObjectInventorySampleParams{BucketID: mustPgUUID(bucket), Token: token}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
