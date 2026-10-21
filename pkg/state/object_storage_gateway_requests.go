package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectStorageGatewayRequestStore = (*MemStore)(nil)
var _ ObjectStorageGatewayRequestStore = (*PgStore)(nil)

func checkGatewayRequest(snapshot ObjectUsageSnapshot, bucket string, at time.Time, p api.ObjectStoragePolicy) error {
	if err := checkGatewayEgress(snapshot, bucket, 0, at, p); err != nil {
		return err
	}
	p = snapshot.planPolicy(p)
	u := SummarizeObjectUsage(snapshot, p, time.Now().UTC())
	if u.RequestCount >= p.MaxMonthlyRequests {
		return &ObjectStorageLimitError{Kind: "requests", Observed: u.RequestCount, Limit: p.MaxMonthlyRequests, Cause: ErrObjectBudget}
	}
	if u.EgressBytes >= p.MaxMonthlyEgressBytes {
		return &ObjectStorageLimitError{Kind: "egress_bytes", Observed: u.EgressBytes, Limit: p.MaxMonthlyEgressBytes, Cause: ErrObjectBudget}
	}
	return nil
}

func (m *MemStore) ReserveObjectStorageGatewayRequest(ctx context.Context, bucket string, at time.Time, p api.ObjectStoragePolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	b, ok := m.objectBuckets[bucket]
	if !ok {
		return ErrNotFound
	}
	if err := checkGatewayRequest(m.objectUsageLocked(m.objectBillingSubjectLocked(b), at), bucket, at, p); err != nil {
		return err
	}
	if m.objectProviderRequests == nil {
		m.objectProviderRequests = map[string]int64{}
	}
	m.objectProviderRequests[objectProviderRequestKey(bucket, at)]++
	return nil
}

func (s *PgStore) ReserveObjectStorageGatewayRequest(ctx context.Context, bucket string, at time.Time, p api.ObjectStoragePolicy) error {
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
	if err = checkGatewayRequest(snapshot, bucket, at, p); err != nil {
		return err
	}
	if err = q.ObjectStorageProviderRequestIncrement(ctx, tx, sqlc.ObjectStorageProviderRequestIncrementParams{BucketID: mustPgUUID(bucket), PeriodStart: objectUsageTime(ObjectStoragePeriod(at))}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
