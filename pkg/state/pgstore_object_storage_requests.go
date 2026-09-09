package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectStorageProviderUsageStore = (*PgStore)(nil)

func (s *PgStore) RecordObjectStorageProviderRequest(ctx context.Context, bucketID string, at time.Time) error {
	if bucketID == "" || at.IsZero() || at.After(time.Now().UTC().Add(time.Minute)) {
		return ErrConflict
	}
	return sqlc.New().ObjectStorageProviderRequestIncrement(ctx, s.pool, sqlc.ObjectStorageProviderRequestIncrementParams{
		BucketID: mustPgUUID(bucketID), PeriodStart: objectUsageTime(ObjectStoragePeriod(at)),
	})
}

func (s *PgStore) ListObjectStorageProviderRequestMetrics(ctx context.Context, backendID, fingerprint string, periodStart time.Time) ([]ObjectStorageProviderRequestMetric, error) {
	rows, err := sqlc.New().ObjectStorageProviderRequestMetrics(ctx, s.pool, sqlc.ObjectStorageProviderRequestMetricsParams{
		BackendID: backendID, BackendFingerprint: fingerprint, PeriodStart: objectUsageTime(ObjectStoragePeriod(periodStart)),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ObjectStorageProviderRequestMetric, 0, len(rows))
	for _, row := range rows {
		out = append(out, ObjectStorageProviderRequestMetric{
			BucketID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), BackendID: row.BackendID,
			BackendFingerprint: row.BackendFingerprint, PhysicalName: row.PhysicalName,
			PeriodStart: row.PeriodStart.Time, RequestCount: row.RequestCount,
		})
	}
	return out, nil
}

func (s *PgStore) ListObjectStorageProviderBuckets(ctx context.Context, backendID, fingerprint string) ([]ObjectBucket, error) {
	rows, err := sqlc.New().ObjectStorageProviderBuckets(ctx, s.pool, sqlc.ObjectStorageProviderBucketsParams{BackendID: backendID, BackendFingerprint: fingerprint})
	if err != nil {
		return nil, err
	}
	out := make([]ObjectBucket, 0, len(rows))
	for _, row := range rows {
		out = append(out, objectBucketFromSQL(row))
	}
	return out, nil
}
