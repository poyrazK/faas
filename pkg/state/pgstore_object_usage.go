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
var _ ObjectStorageBillingStore = (*PgStore)(nil)
var _ ObjectInventoryReleaser = (*PgStore)(nil)
var _ ObjectBucketPlanLimitsStore = (*PgStore)(nil)

func objectUsageTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func (s *pgCore) ObjectUsage(ctx context.Context, account string, now time.Time) (ObjectUsageSnapshot, error) {
	return readObjectUsage(ctx, s.pool, account, ObjectStoragePeriod(now))
}

func readObjectUsage(ctx context.Context, db sqlc.DBTX, account string, now time.Time) (ObjectUsageSnapshot, error) {
	return readObjectUsageForPeriod(ctx, db, account, ObjectStoragePeriod(now))
}

func (s *PgStore) ObjectUsageForPeriod(ctx context.Context, account string, periodStart time.Time) (ObjectUsageSnapshot, error) {
	return readObjectUsageForPeriod(ctx, s.pool, account, ObjectStoragePeriod(periodStart))
}

// Lock financial ownership before its retained namespace. All quota, inventory,
// transfer and configuration writers use this order across legacy namespaces.
func lockObjectUsageScope(ctx context.Context, tx pgx.Tx, account, bucket pgtype.UUID) (pgtype.UUID, error) {
	q := sqlc.New()
	if _, err := q.ObjectStorageLockScope(ctx, tx, sqlc.ObjectStorageLockScopeParams{BucketID: bucket, AccountID: account}); err != nil {
		return pgtype.UUID{}, mapErr(err)
	}
	if err := q.ObjectStorageLockLegacyIdentity(ctx, tx, account); err != nil {
		return pgtype.UUID{}, mapErr(err)
	}
	return q.ObjectUsageLockAccount(ctx, tx, account)
}

// ObjectBucketPlanLimits returns the object-storage limits of the plan that
// pays for the bucket now (ADR-20261111123000 decision 8).
func (s *pgCore) ObjectBucketPlanLimits(ctx context.Context, account, bucket string) (api.ObjectStoragePlanLimits, error) {
	if !validUUID(account) || !validUUID(bucket) {
		return api.ObjectStoragePlanLimits{}, ErrNotFound
	}
	subject, err := sqlc.New().ObjectStorageSubject(ctx, s.pool, sqlc.ObjectStorageSubjectParams{BucketID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	if err != nil {
		return api.ObjectStoragePlanLimits{}, mapErr(err)
	}
	return readObjectPlanLimits(ctx, s.pool, pgUUIDString(subject))
}

// readObjectPlanLimits returns a billing subject's object-storage plan
// limits. ADR-1320: checks tighten the operator policy by the plan's
// ceilings. A missing account keeps the operator policy, as before.
func readObjectPlanLimits(ctx context.Context, db sqlc.DBTX, subject string) (api.ObjectStoragePlanLimits, error) {
	owner, err := sqlc.New().ObjectUsageAccountPlanCap(ctx, db, mustPgUUID(subject))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return api.ObjectStoragePlanLimits{}, err
	}
	var capCents, grantBytes *int64
	if owner.OverageCapCents.Valid {
		capCents = &owner.OverageCapCents.Int64
	}
	if owner.CeilingGrantBytes.Valid {
		grantBytes = &owner.CeilingGrantBytes.Int64
	}
	return objectStoragePlanLimits(api.Plan(owner.Plan), grantBytes, capCents), nil
}

// Storage namespace selects the bucket; financial ownership selects its quota pool.
func readObjectBucketUsage(ctx context.Context, db sqlc.DBTX, account, bucket string, now time.Time) (ObjectUsageSnapshot, error) {
	subject, err := sqlc.New().ObjectStorageSubject(ctx, db, sqlc.ObjectStorageSubjectParams{BucketID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	if err != nil {
		return ObjectUsageSnapshot{}, mapErr(err)
	}
	return readObjectUsage(ctx, db, pgUUIDString(subject), now)
}

func readObjectUsageForPeriod(ctx context.Context, db sqlc.DBTX, account string, periodStart time.Time) (ObjectUsageSnapshot, error) {
	q := sqlc.New()
	out := ObjectUsageSnapshot{}
	limits, err := readObjectPlanLimits(ctx, db, account)
	if err != nil {
		return out, err
	}
	out.PlanLimits = limits
	rows, err := q.ObjectUsageBuckets(ctx, db, sqlc.ObjectUsageBucketsParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(ObjectStoragePeriod(periodStart))})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Buckets = append(out.Buckets, ObjectBucketUsage{
			Bucket:         ObjectBucket{ID: pgUUIDString(r.ID), OrgID: pgUUIDString(r.OrgID), AccountID: pgUUIDString(r.AccountID), AppID: pgUUIDString(r.AppID), BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, PhysicalName: r.PhysicalName, State: r.State, PublicRead: r.PublicRead, ServeAt: r.ServeAt.String, CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time},
			MultipartBytes: r.MultipartBytes, InventoryScope: r.InventoryScope.String, BaselineBytes: r.BaselineBytes.Int64, BaselineKeys: r.BaselineKeys.Int64, GrantedBytes: r.GrantedBytes.Int64, GrantedKeys: r.GrantedKeys.Int64,
			ObservedBytes: r.ObservedBytes.Int64, ObservedKeys: r.ObservedKeys.Int64, ObservedAt: r.ObservedAt.Time, AttemptAt: r.AttemptAt.Time, LeaseUntil: r.InventoryLeaseUntil.Time, Token: r.Token.String,
			RequestCount: r.GatewayRequestCount, EgressBytes: r.GatewayEgressBytes, GatewayMetricsKnown: true,
			StoredByteHours: r.GatewayStoredByteHours, StorageMeteredThrough: r.GatewayStorageThrough.Time,
			InventoryScan: ObjectInventoryScan{StartedAt: r.ScanStartedAt.Time, ProgressAt: r.ScanProgressAt.Time, Bytes: r.ScanBytes.Int64, Keys: r.ScanKeys.Int64, Pages: int(r.ScanPages.Int32), Complete: r.ScanComplete.Bool},
			InventoryHeld: r.InventoryHeld,
		})
	}
	reports, err := q.ObjectUsageReports(ctx, db, sqlc.ObjectUsageReportsParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(periodStart)})
	if err != nil {
		return out, err
	}
	for _, r := range reports {
		out.Reports = append(out.Reports, objectReportFromSQL(r))
	}
	out.Authorizations, err = q.ObjectUsageAuthorizationCount(ctx, db, sqlc.ObjectUsageAuthorizationCountParams{AccountID: mustPgUUID(account), PeriodStart: objectUsageTime(periodStart)})
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	return out, err
}

func objectReportFromSQL(r sqlc.ObjectStorageUsageReport) api.ObjectStorageUsageReport {
	return api.ObjectStorageUsageReport{AccountID: pgUUIDString(r.AccountID), BackendID: r.BackendID, BackendFingerprint: r.BackendFingerprint, Source: r.Source, PeriodStart: r.PeriodStart.Time, ObservedAt: r.ObservedAt.Time, StoredByteHours: r.StoredByteHours, RequestCount: r.RequestCount, EgressBytes: r.EgressBytes, CostMillicents: r.CostMillicents}
}

func (s *pgCore) AdmitObjectURL(ctx context.Context, account, bucket, key string, size int64, put bool, p api.ObjectStoragePolicy) error {
	return s.admitObjectURL(ctx, account, bucket, key, size, put, p, "")
}
func (s *pgCore) admitObjectURL(ctx context.Context, account, bucket, key string, size int64, put bool, p api.ObjectStoragePolicy, token string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err = admitObjectURLTx(ctx, tx, account, bucket, key, size, put, p, token, false); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// The caller owns the transaction; intent creation and admission commit together.
func admitObjectURLTx(ctx context.Context, tx pgx.Tx, account, bucket, key string, size int64, put bool, p api.ObjectStoragePolicy, token string, route bool) error {
	return admitObjectWriteSourceTx(ctx, tx, account, bucket, key, size, put, p, token, route, "")
}

func admitObjectWriteSourceTx(ctx context.Context, tx pgx.Tx, account, bucket, key string, size int64, put bool, p api.ObjectStoragePolicy, token string, route bool, multipartID string) error {
	q := sqlc.New()
	owner, ownerErr := q.ObjectStorageLockOwner(ctx, tx, sqlc.ObjectStorageLockOwnerParams{BucketID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
	if ownerErr != nil {
		return mapErr(ownerErr)
	}
	subject := pgUUIDString(owner.Subject)
	if _, err := lockObjectUsageScope(ctx, tx, mustPgUUID(account), mustPgUUID(bucket)); err != nil {
		return mapErr(err)
	}
	if put {
		if err := checkObjectWriteKeyTx(ctx, tx, bucket, key, token); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	snapshot, err := readObjectUsage(ctx, tx, subject, now)
	if err != nil {
		return err
	}
	old, err := q.ObjectUsageGrant(ctx, tx, sqlc.ObjectUsageGrantParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key)})
	exists := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	all := versionAdmissionMode(snapshot, bucket)
	if put {
		mode, e := q.ObjectVersionAccountingStatus(ctx, tx, sqlc.ObjectVersionAccountingStatusParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
		if e != nil {
			return mapErr(e)
		}
		if mode.VersionsObserved && !all || all && (token == "" || !route && multipartID == "") {
			return ErrConflict
		}
	}
	if all {
		old = 0
		exists = false
	}
	delta, keys, err := checkObjectAdmission(snapshot, bucket, size, old, exists, put, p, now)
	if err != nil {
		return err
	}
	if put {
		// Only a deletion of this key fences the write (ADR-20261025115958).
		fenced, e := q.ObjectCapacityFencedForKey(ctx, tx, sqlc.ObjectCapacityFencedForKeyParams{BucketID: mustPgUUID(bucket), ObjectKey: key})
		if e != nil {
			return e
		}
		if fenced {
			return ErrConflict
		}
		if token != "" {
			kind := "proxy"
			var session pgtype.UUID
			if multipartID != "" {
				kind, session = "multipart", mustPgUUID(multipartID)
			}
			if err = q.ObjectWriteInsert(ctx, tx, sqlc.ObjectWriteInsertParams{ID: mustPgUUID(token), BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), Kind: kind, MultipartUploadID: session, RouteReceipt: route, NativeVersion: all, NativeBytes: nativeGrantBytes(all, size)}); err != nil {
				return mapErr(err)
			}
			if !all {
				err = q.ObjectTrackedGrantUpsert(ctx, tx, sqlc.ObjectTrackedGrantUpsertParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), MaxBytes: size, LastWriteID: mustPgUUID(token)})
			}
		} else {
			err = q.ObjectUsageGrantUpsert(ctx, tx, sqlc.ObjectUsageGrantUpsertParams{BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), MaxBytes: size})
		}
		if err != nil {
			return mapErr(err)
		}
		if err = q.ObjectUsageGrantIncrement(ctx, tx, sqlc.ObjectUsageGrantIncrementParams{BucketID: mustPgUUID(bucket), GrantedBytes: delta, GrantedKeys: keys}); err != nil {
			return err
		}
	}
	if err = q.ObjectUsageAuthorize(ctx, tx, sqlc.ObjectUsageAuthorizeParams{AccountID: mustPgUUID(subject), PeriodStart: objectUsageTime(ObjectStoragePeriod(now))}); err != nil {
		return err
	}
	return nil
}

func (s *pgCore) RecordObjectUsageReport(ctx context.Context, r api.ObjectStorageUsageReport) error {
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
	if _, err = q.ObjectStorageLockReportingOwners(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return mapErr(err)
	}
	if err = q.ObjectStorageLockLegacyIdentity(ctx, tx, mustPgUUID(r.AccountID)); err != nil {
		return mapErr(err)
	}
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
	cutover, err := q.ObjectStorageReportCutover(ctx, tx, sqlc.ObjectStorageReportCutoverParams{Subject: mustPgUUID(r.AccountID), Period: objectUsageTime(r.PeriodStart)})
	if err != nil {
		return mapErr(err)
	}
	if cutover.Valid && r.ObservedAt.Before(cutover.Time) {
		return ErrConflict
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

func (s *pgCore) DueObjectInventories(ctx context.Context, limit int32) ([]ObjectBucket, error) {
	due, err := s.DueObjectInventoryQueue(ctx, limit, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectBucket, 0, len(due))
	for _, d := range due {
		out = append(out, d.Bucket)
	}
	return out, nil
}

func (s *pgCore) DueObjectInventoryQueue(ctx context.Context, limit int32, skip []string) ([]ObjectInventoryDue, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrConflict
	}
	ids := make([]pgtype.UUID, 0, len(skip))
	for _, id := range skip {
		ids = append(ids, mustPgUUID(id))
	}
	rows, err := sqlc.New().ObjectInventoriesDue(ctx, s.pool, sqlc.ObjectInventoriesDueParams{LongKeys: api.ObjectStorageInventoryLongListingKeys, ScanMaxAgeSeconds: int64(api.ObjectInventoryScanMaxAge / time.Second), Skip: ids, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	out := make([]ObjectInventoryDue, 0, len(rows))
	for _, r := range rows {
		out = append(out, ObjectInventoryDue{Bucket: objectBucketFromSQL(r.ObjectBucket), Long: r.LongListing})
	}
	return out, nil
}

func (s *pgCore) ObjectInventoryBacklog(ctx context.Context) (ObjectInventoryBacklog, error) {
	r, err := sqlc.New().ObjectInventoryBacklog(ctx, s.pool, sqlc.ObjectInventoryBacklogParams{ScanMaxAgeSeconds: int64(api.ObjectInventoryScanMaxAge / time.Second), ProgressWindowSeconds: int64(api.ObjectInventoryScanProgressWindow / time.Second)})
	if err != nil {
		return ObjectInventoryBacklog{}, err
	}
	return ObjectInventoryBacklog{DueBuckets: r.DueBuckets, OldestObservationAge: time.Duration(max(r.OldestAgeSeconds, 0) * float64(time.Second))}, nil
}

func (s *pgCore) ClaimObjectInventory(ctx context.Context, bucket, token string) error {
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

func (s *pgCore) ReleaseObjectInventory(ctx context.Context, bucket, token string) error {
	if token == "" {
		return ErrConflict
	}
	n, err := sqlc.New().ObjectInventoryRelease(ctx, s.pool, sqlc.ObjectInventoryReleaseParams{BucketID: mustPgUUID(bucket), Token: token})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func (s *pgCore) FinishObjectInventory(ctx context.Context, bucket, token string, bytes, objects int64) error {
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
	if _, err = lockObjectUsageScope(ctx, tx, account, mustPgUUID(bucket)); err != nil {
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

// Account locking in the caller serializes admission with settlement.
func checkObjectWriteKeyTx(ctx context.Context, tx pgx.Tx, bucket, key, own string) error {
	var ownID pgtype.UUID
	if own != "" {
		ownID = mustPgUUID(own)
	}
	fenced, err := sqlc.New().ObjectWriteKeyFenced(ctx, tx, sqlc.ObjectWriteKeyFencedParams{
		BucketID: mustPgUUID(bucket), KeyHash: objectKeyHash(key), OwnWrite: ownID,
	})
	if err != nil {
		return err
	}
	if fenced {
		return ErrConflict
	}
	return nil
}
