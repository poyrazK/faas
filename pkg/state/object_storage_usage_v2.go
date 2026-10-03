package state

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectStorageCustomerUsageV2Store is a shadow-only import/read seam. It is
// intentionally not embedded in the live admission or billing store: writing
// v2 evidence cannot authorize a URL or create a customer charge.
type ObjectStorageCustomerUsageV2Store interface {
	RecordObjectCustomerUsageV2(context.Context, api.ObjectStorageCustomerUsageReportV2) error
	LatestObjectCustomerUsageV2(context.Context, string, string, time.Time) (api.ObjectStorageCustomerUsageReportV2, error)
}

func normalizeObjectCustomerUsageV2(r api.ObjectStorageCustomerUsageReportV2) api.ObjectStorageCustomerUsageReportV2 {
	r.PeriodStart = r.PeriodStart.UTC()
	r.CoverageEnd = r.CoverageEnd.UTC().Truncate(time.Microsecond)
	r.ObservedAt = r.ObservedAt.UTC().Truncate(time.Microsecond)
	if r.EgressBytes != nil {
		v := *r.EgressBytes
		r.EgressBytes = &v
	}
	return r
}

func validObjectCustomerUsageV2(r api.ObjectStorageCustomerUsageReportV2, now time.Time) bool {
	if _, err := uuid.Parse(r.AccountID); err != nil {
		return false
	}
	if len(r.BackendID) < 1 || len(r.BackendID) > 63 || len(r.Source) < 1 || len(r.Source) > 128 || r.Version != api.ObjectStorageCustomerUsageVersion {
		return false
	}
	for _, digest := range []string{r.BackendFingerprint, r.EvidenceDigest} {
		if len(digest) != 64 {
			return false
		}
		decoded, err := hex.DecodeString(digest)
		if err != nil || hex.EncodeToString(decoded) != digest {
			return false
		}
	}
	if r.PeriodStart.IsZero() || !r.PeriodStart.Equal(ObjectStoragePeriod(r.PeriodStart)) || r.CoverageEnd.Before(r.PeriodStart) || r.CoverageEnd.After(r.PeriodStart.AddDate(0, 1, 0)) || r.ObservedAt.Before(r.CoverageEnd) || r.ObservedAt.After(now) {
		return false
	}
	for _, v := range []int64{r.StoredByteHours, r.ReadOperations, r.WriteOperations} {
		if v < 0 || v > api.MaxObjectStoragePolicyValue {
			return false
		}
	}
	return r.EgressBytes == nil || (*r.EgressBytes >= 0 && *r.EgressBytes <= api.MaxObjectStoragePolicyValue)
}

func sameObjectCustomerUsageV2(a, b api.ObjectStorageCustomerUsageReportV2) bool {
	a, b = normalizeObjectCustomerUsageV2(a), normalizeObjectCustomerUsageV2(b)
	if (a.EgressBytes == nil) != (b.EgressBytes == nil) {
		return false
	}
	if a.EgressBytes != nil {
		if *a.EgressBytes != *b.EgressBytes {
			return false
		}
		a.EgressBytes, b.EgressBytes = nil, nil
	}
	return a == b
}

func objectCustomerUsageV2Advances(old, next api.ObjectStorageCustomerUsageReportV2) bool {
	if !next.ObservedAt.After(old.ObservedAt) || next.CoverageEnd.Before(old.CoverageEnd) || old.BackendFingerprint != next.BackendFingerprint || old.Source != next.Source || next.StoredByteHours < old.StoredByteHours || next.ReadOperations < old.ReadOperations || next.WriteOperations < old.WriteOperations {
		return false
	}
	if old.EgressBytes != nil && (next.EgressBytes == nil || *next.EgressBytes < *old.EgressBytes) {
		return false
	}
	return true
}

func objectCustomerUsageV2Placement(s ObjectUsageSnapshot, r api.ObjectStorageCustomerUsageReportV2) bool {
	periodEnd := r.PeriodStart.AddDate(0, 1, 0)
	for _, b := range s.Buckets {
		if b.Bucket.BackendID == r.BackendID && b.Bucket.BackendFingerprint == r.BackendFingerprint &&
			(b.Bucket.CreatedAt.IsZero() || b.Bucket.CreatedAt.Before(periodEnd)) &&
			(b.Bucket.State != "deleted" || b.Bucket.UpdatedAt.IsZero() || b.Bucket.UpdatedAt.After(r.PeriodStart)) {
			return true
		}
	}
	return false
}
