package state

import (
	"context"
	"time"
)

// ObjectStorageProviderRequestMetric is a cumulative count of outbound
// provider request attempts for one physical bucket and UTC month. It is
// deliberately tied to the immutable bucket catalog so a later usage export
// cannot attribute traffic from an unknown physical bucket to a tenant.
type ObjectStorageProviderRequestMetric struct {
	BucketID           string
	AccountID          string
	BackendID          string
	BackendFingerprint string
	PhysicalName       string
	PeriodStart        time.Time
	RequestCount       int64
	EgressBytes        int64
}

// ObjectStorageProviderUsageStore is the narrow persistence seam shared by
// the branded gateway and provider usage exporters. Production uses Postgres;
// MemStore implements the same contract for parity tests.
type ObjectStorageProviderUsageStore interface {
	RecordObjectStorageProviderRequest(context.Context, string, time.Time) error
	ListObjectStorageProviderRequestMetrics(context.Context, string, string, time.Time) ([]ObjectStorageProviderRequestMetric, error)
	ListObjectStorageProviderBuckets(context.Context, string, string) ([]ObjectBucket, error)
}

// ObjectStorageProviderEgressStore is an optional extension implemented by
// production stores. Keeping it separate preserves compatibility with small
// request-metric test doubles and older exporters.
type ObjectStorageProviderEgressStore interface {
	RecordObjectStorageProviderEgress(context.Context, string, int64, time.Time) error
}
