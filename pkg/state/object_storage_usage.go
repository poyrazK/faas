package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrObjectUsageStale = errors.New("object storage usage is unavailable or stale")
	ErrObjectBudget     = errors.New("object storage budget reached")
	ErrObjectCapacity   = errors.New("object storage capacity reserved")
)

type ObjectStorageLimitError struct {
	Kind            string
	Limit, Observed int64
	Cause           error
}

func (e *ObjectStorageLimitError) Error() string { return e.Kind + ": " + e.Cause.Error() }
func (e *ObjectStorageLimitError) Unwrap() error { return e.Cause }

type ObjectBucketUsage struct {
	Bucket                                                 ObjectBucket
	BaselineBytes, BaselineKeys, GrantedBytes, GrantedKeys int64
	ObservedBytes, ObservedKeys                            int64
	ObservedAt, AttemptAt, LeaseUntil                      time.Time
	Token                                                  string
}

type ObjectUsageSnapshot struct {
	Buckets        []ObjectBucketUsage
	Reports        []api.ObjectStorageUsageReport
	Authorizations int64
}

type ObjectStorageAccountingStore interface {
	ObjectUsage(context.Context, string, time.Time) (ObjectUsageSnapshot, error)
	AdmitObjectURL(context.Context, string, string, string, int64, bool, api.ObjectStoragePolicy) error
	RecordObjectUsageReport(context.Context, api.ObjectStorageUsageReport) error
	DueObjectInventories(context.Context, int32) ([]ObjectBucket, error)
	ClaimObjectInventory(context.Context, string, string) error
	FinishObjectInventory(context.Context, string, string, int64, int64) error
}

func ObjectStoragePeriod(now time.Time) time.Time {
	now = now.UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func ValidObjectStoragePolicy(p api.ObjectStoragePolicy) bool {
	return p.Valid()
}

func objectKeyHash(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

func boundedObjectAdd(a, b int64) int64 {
	// Saturation is deliberately fail-closed for policy comparisons.
	if b < 0 || a > api.MaxObjectStoragePolicyValue-b {
		return api.MaxObjectStoragePolicyValue + 1
	}
	return a + b
}

func SummarizeObjectUsage(s ObjectUsageSnapshot, p api.ObjectStoragePolicy, now time.Time) api.ObjectStorageUsage {
	u := api.ObjectStorageUsage{Fresh: ValidObjectStoragePolicy(p), PeriodStart: ObjectStoragePeriod(now), Authorizations: s.Authorizations}
	required := map[string]string{}
	for _, b := range s.Buckets {
		if b.Bucket.State != "deleted" || !b.Bucket.UpdatedAt.Before(u.PeriodStart) {
			required[b.Bucket.BackendID] = b.Bucket.BackendFingerprint
		}
		if b.Bucket.State == "deleted" {
			continue
		}
		u.ObservedBytes = boundedObjectAdd(u.ObservedBytes, b.ObservedBytes)
		u.CapacityBytes = boundedObjectAdd(u.CapacityBytes, max(b.ObservedBytes, boundedObjectAdd(b.BaselineBytes, b.GrantedBytes)))
		u.CapacityKeys = boundedObjectAdd(u.CapacityKeys, max(b.ObservedKeys, boundedObjectAdd(b.BaselineKeys, b.GrantedKeys)))
		if b.ObservedAt.IsZero() || b.ObservedAt.After(now) || now.Sub(b.ObservedAt) > time.Duration(api.ObjectStorageInventoryMaxAgeSeconds)*time.Second {
			u.Fresh = false
		}
	}
	for _, r := range s.Reports {
		if !r.PeriodStart.Equal(u.PeriodStart) {
			continue
		}
		u.StoredByteHours = boundedObjectAdd(u.StoredByteHours, r.StoredByteHours)
		u.RequestCount = boundedObjectAdd(u.RequestCount, r.RequestCount)
		u.EgressBytes = boundedObjectAdd(u.EgressBytes, r.EgressBytes)
		u.CostMillicents = boundedObjectAdd(u.CostMillicents, r.CostMillicents)
		if r.ObservedAt.After(now) || now.Sub(r.ObservedAt) > time.Duration(p.MaxReportAgeSeconds)*time.Second {
			u.Fresh = false
		}
		if fp, ok := required[r.BackendID]; ok && fp == r.BackendFingerprint {
			delete(required, r.BackendID)
		}
	}
	if len(required) != 0 {
		u.Fresh = false
	}
	return u
}

func checkObjectAdmission(s ObjectUsageSnapshot, bucketID string, size, oldSize int64, exists, put bool, p api.ObjectStoragePolicy, now time.Time) (int64, int64, error) {
	u := SummarizeObjectUsage(s, p, now)
	if !u.Fresh {
		return 0, 0, ErrObjectUsageStale
	}
	for _, limit := range []struct {
		name          string
		observed, max int64
	}{
		{"cost_millicents", u.CostMillicents, p.MaxMonthlyCostMillicents},
		{"requests", u.RequestCount, p.MaxMonthlyRequests},
		{"egress_bytes", u.EgressBytes, p.MaxMonthlyEgressBytes},
		{"authorizations", u.Authorizations, p.MaxMonthlyAuthorizations},
	} {
		if limit.observed >= limit.max {
			return 0, 0, &ObjectStorageLimitError{Kind: limit.name, Observed: limit.observed, Limit: limit.max, Cause: ErrObjectBudget}
		}
	}
	var found *ObjectBucketUsage
	for i := range s.Buckets {
		if s.Buckets[i].Bucket.ID == bucketID {
			found = &s.Buckets[i]
			break
		}
	}
	if found == nil || found.Bucket.State != "ready" {
		return 0, 0, ErrConflict
	}
	if !put {
		return 0, 0, nil
	}
	if size < 0 || size > api.MaxObjectUploadBytes {
		return 0, 0, ErrConflict
	}
	delta, keys := max(int64(0), size-oldSize), int64(0)
	if !exists {
		keys = 1
	}
	bucketCapacity := max(found.ObservedBytes, boundedObjectAdd(found.BaselineBytes, found.GrantedBytes))
	for _, limit := range []struct {
		name          string
		observed, max int64
	}{
		{"account_bytes", boundedObjectAdd(u.CapacityBytes, delta), p.MaxAccountBytes},
		{"bucket_bytes", boundedObjectAdd(bucketCapacity, delta), p.MaxBucketBytes},
		{"account_keys", boundedObjectAdd(u.CapacityKeys, keys), p.MaxAccountKeys},
	} {
		if limit.observed > limit.max {
			return 0, 0, &ObjectStorageLimitError{Kind: limit.name, Observed: limit.observed, Limit: limit.max, Cause: ErrObjectCapacity}
		}
	}
	return delta, keys, nil
}

func validObjectReport(r api.ObjectStorageUsageReport, now time.Time) bool {
	if _, err := uuid.Parse(r.AccountID); err != nil {
		return false
	}
	if _, err := hex.DecodeString(r.BackendFingerprint); err != nil {
		return false
	}
	if r.AccountID == "" || r.BackendID == "" || len(r.BackendID) > 63 || len(r.BackendFingerprint) != 64 || r.Source == "" || len(r.Source) > 128 || !r.PeriodStart.Equal(ObjectStoragePeriod(r.ObservedAt)) || r.ObservedAt.After(now) {
		return false
	}
	for _, v := range []int64{r.StoredByteHours, r.RequestCount, r.EgressBytes, r.CostMillicents} {
		if v < 0 || v > api.MaxObjectStoragePolicyValue {
			return false
		}
	}
	return true
}

func objectReportAdvances(old, next api.ObjectStorageUsageReport) bool {
	return next.ObservedAt.After(old.ObservedAt) && old.BackendFingerprint == next.BackendFingerprint && old.Source == next.Source && next.StoredByteHours >= old.StoredByteHours && next.RequestCount >= old.RequestCount && next.EgressBytes >= old.EgressBytes && next.CostMillicents >= old.CostMillicents
}

func normalizeObjectReport(r api.ObjectStorageUsageReport) api.ObjectStorageUsageReport {
	r.PeriodStart = r.PeriodStart.UTC()
	r.ObservedAt = r.ObservedAt.UTC().Truncate(time.Microsecond)
	return r
}

func sameObjectReport(a, b api.ObjectStorageUsageReport) bool {
	a = normalizeObjectReport(a)
	b = normalizeObjectReport(b)
	return a == b
}

func checkObjectReport(s ObjectUsageSnapshot, r api.ObjectStorageUsageReport) error {
	matched := false
	for _, b := range s.Buckets {
		if b.Bucket.BackendID == r.BackendID && b.Bucket.BackendFingerprint == r.BackendFingerprint {
			matched = true
		}
	}
	if !matched {
		return ErrNotFound
	}
	for _, old := range s.Reports {
		if old.BackendID == r.BackendID && !objectReportAdvances(old, r) {
			return ErrConflict
		}
	}
	return nil
}
