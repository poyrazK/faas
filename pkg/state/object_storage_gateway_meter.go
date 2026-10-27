package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectStorageGatewayEgressStore = (*MemStore)(nil)
var _ ObjectStorageGatewayEgressStore = (*PgStore)(nil)

func checkGatewayEgress(s ObjectUsageSnapshot, bucket string, bytes int64, at time.Time, p api.ObjectStoragePolicy) error {
	now := time.Now().UTC()
	if !p.Valid() || !p.GatewaySafety() || bytes < 0 || bytes > api.MaxObjectStoragePolicyValue || at.IsZero() || at.After(now.Add(time.Minute)) || !ObjectStoragePeriod(at).Equal(ObjectStoragePeriod(now)) {
		return ErrObjectUsageStale
	}
	p = s.planPolicy(p)
	u, stale := summarizeObjectUsage(s, p, now)
	if !u.Fresh || stale[bucket] {
		return ErrObjectUsageStale
	}
	matched := false
	for _, b := range s.Buckets {
		if b.Bucket.ID == bucket && b.Bucket.State == "ready" {
			matched = true
		}
	}
	if !matched {
		return ErrConflict
	}
	if observed := boundedObjectAdd(u.EgressBytes, bytes); observed > p.MaxMonthlyEgressBytes {
		return &ObjectStorageLimitError{Kind: "egress_bytes", Limit: p.MaxMonthlyEgressBytes, Observed: observed, Cause: ErrObjectBudget}
	}
	return nil
}

func (m *MemStore) ReserveObjectStorageGatewayEgress(ctx context.Context, bucket string, bytes int64, at time.Time, p api.ObjectStoragePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	b, ok := m.objectBuckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if err := checkGatewayEgress(m.objectUsageLocked(m.objectBillingSubjectLocked(b), at), bucket, bytes, at, p); err != nil {
		return err
	}
	if m.objectProviderRequests == nil {
		m.objectProviderRequests = map[string]int64{}
	}
	m.objectProviderRequests[objectProviderRequestKey(bucket, at)+"\x00egress"] += bytes
	return nil
}

func (s *pgCore) ReserveObjectStorageGatewayEgress(ctx context.Context, bucket string, bytes int64, at time.Time, p api.ObjectStoragePolicy) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	account, err := q.ObjectUsageBucketAccount(ctx, tx, mustPgUUID(bucket))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	owner, ownerErr := q.ObjectStorageLockOwner(ctx, tx, sqlc.ObjectStorageLockOwnerParams{BucketID: mustPgUUID(bucket), AccountID: account})
	if ownerErr != nil {
		return mapErr(ownerErr)
	}
	if _, err = lockObjectUsageScope(ctx, tx, account, mustPgUUID(bucket)); err != nil {
		return err
	}
	snapshot, err := readObjectUsage(ctx, tx, pgUUIDString(owner.Subject), at)
	if err != nil {
		return err
	}
	if err = checkGatewayEgress(snapshot, bucket, bytes, at, p); err != nil {
		return err
	}
	if err = q.ObjectStorageProviderEgressIncrement(ctx, tx, sqlc.ObjectStorageProviderEgressIncrementParams{BucketID: mustPgUUID(bucket), PeriodStart: objectUsageTime(ObjectStoragePeriod(at)), EgressBytes: bytes}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
