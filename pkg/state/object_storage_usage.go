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
	ErrObjectUsageStale        = errors.New("object storage usage is unavailable or stale")
	ErrObjectBudget            = errors.New("object storage budget reached")
	ErrObjectCapacity          = errors.New("object storage capacity reserved")
	ErrObjectBillingOpen       = errors.New("object storage billing period is not closed")
	ErrObjectBillingIncomplete = errors.New("object storage billing period is incomplete")
	ErrObjectBillingConflict   = errors.New("object storage billing period conflicts with an existing finalization")
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
	MultipartBytes                                         int64
	ObservedAt, AttemptAt, LeaseUntil                      time.Time
	Token                                                  string
	InventoryScope                                         string
	RequestCount, EgressBytes                              int64
	GatewayMetricsKnown                                    bool
	// StoredByteHours is the bucket's gateway-metered storage for the
	// snapshot's month, complete to StorageMeteredThrough. A zero
	// StorageMeteredThrough means Gregale has not metered it this month
	// (ADR-20261024132844).
	StoredByteHours       int64
	StorageMeteredThrough time.Time
}

type ObjectUsageSnapshot struct {
	Buckets        []ObjectBucketUsage
	Reports        []api.ObjectStorageUsageReport
	Authorizations int64
	// PlanLimits are the object-storage limits of the account's plan when
	// the snapshot was read. Admission and gateway checks tighten the
	// operator policy by their ceilings (ADR-1320); zero, as for an unknown
	// plan, keeps the operator policy.
	PlanLimits api.ObjectStoragePlanLimits
}

// objectStoragePlanLimits returns the plan's object-storage limits, or none
// for an unknown plan.
func objectStoragePlanLimits(plan api.Plan) api.ObjectStoragePlanLimits {
	limits, _ := api.LimitsFor(plan)
	return limits.ObjectStorage
}

// planPolicy is the policy every admission and gateway check enforces: the
// operator safety policy tightened by the account plan's ceilings (ADR-1320).
func (s ObjectUsageSnapshot) planPolicy(p api.ObjectStoragePolicy) api.ObjectStoragePolicy {
	return s.PlanLimits.Policy(p)
}

type ObjectStorageAccountingStore interface {
	ObjectUsage(context.Context, string, time.Time) (ObjectUsageSnapshot, error)
	AdmitObjectURL(context.Context, string, string, string, int64, bool, api.ObjectStoragePolicy) error
	RecordObjectUsageReport(context.Context, api.ObjectStorageUsageReport) error
	DueObjectInventories(context.Context, int32) ([]ObjectBucket, error)
	ClaimObjectInventory(context.Context, string, string) error
	FinishObjectInventory(context.Context, string, string, int64, int64) error
}

// ObjectInventoryReleaser gives up an inventory claim without publishing it,
// so deletions waiting for the claim continue at once (ADR-20261025115958).
// The claim's attempt time stays, which keeps the retry cadence.
type ObjectInventoryReleaser interface {
	ReleaseObjectInventory(ctx context.Context, bucket, token string) error
}

// inventoryClaimYields reports an inventory claim young enough that new
// deletions in its bucket wait for it (ADR-20261025115958).
func inventoryClaimYields(u ObjectBucketUsage, now time.Time) bool {
	return u.Token != "" && u.LeaseUntil.After(now) && now.Before(u.AttemptAt.Add(api.ObjectInventoryDeletionYield))
}

// ObjectStorageBillingStore is the persistence seam for month-close billing.
// It is deliberately separate from ObjectStorageAccountingStore so existing
// accounting test doubles and deployments can adopt billing independently.
type ObjectStorageBillingStore interface {
	ObjectUsageForPeriod(context.Context, string, time.Time) (ObjectUsageSnapshot, error)
	GetObjectStorageBillingPeriod(context.Context, string, time.Time) (ObjectStorageBillingRecord, error)
	RecordObjectStorageBillingPeriod(context.Context, ObjectStorageBillingRecord) (ObjectStorageBillingRecord, error)
	GetObjectStorageBillingDelivery(context.Context, string, string) (ObjectStorageBillingDelivery, error)
	RecordObjectStorageBillingDelivery(context.Context, ObjectStorageBillingDelivery) (ObjectStorageBillingDelivery, error)
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

// SummarizeObjectUsage reduces an account's usage snapshot. Fresh is false
// when an account-wide meter input is missing or stale, or when any bucket's
// inventory is stale. Admission checks use summarizeObjectUsage instead, which
// confines a stale inventory to its own bucket.
func SummarizeObjectUsage(s ObjectUsageSnapshot, p api.ObjectStoragePolicy, now time.Time) api.ObjectStorageUsage {
	u, stale := summarizeObjectUsage(s, p, now)
	if len(stale) != 0 {
		u.Fresh = false
	}
	return u
}

// summarizeObjectUsage reports the account's meters with Fresh covering only
// account-wide inputs (policy, provider reports, gateway metering coverage),
// and returns the live buckets whose inventory is missing or older than
// ObjectStorageInventoryMaxAgeSeconds. A stale bucket still counts toward the
// account totals with its conservative capacity: the larger of its last
// observation and baseline plus grants, plus open multipart reservations.
func summarizeObjectUsage(s ObjectUsageSnapshot, p api.ObjectStoragePolicy, now time.Time) (api.ObjectStorageUsage, map[string]bool) {
	u := api.ObjectStorageUsage{Fresh: ValidObjectStoragePolicy(p), PeriodStart: ObjectStoragePeriod(now), Authorizations: s.Authorizations}
	stale := map[string]bool{}
	if p.GatewaySafety() {
		return summarizeGatewaySafety(s, p, now, u, stale)
	}
	required := map[string]string{}
	for _, b := range s.Buckets {
		if b.Bucket.State != "deleted" || !b.Bucket.UpdatedAt.Before(u.PeriodStart) {
			required[b.Bucket.BackendID] = b.Bucket.BackendFingerprint
		}
		if b.Bucket.State == "deleted" {
			continue
		}
		u.ObservedBytes = boundedObjectAdd(u.ObservedBytes, b.ObservedBytes)
		u.CapacityBytes = boundedObjectAdd(u.CapacityBytes, boundedObjectAdd(max(b.ObservedBytes, boundedObjectAdd(b.BaselineBytes, b.GrantedBytes)), b.MultipartBytes))
		u.CapacityKeys = boundedObjectAdd(u.CapacityKeys, max(b.ObservedKeys, boundedObjectAdd(b.BaselineKeys, b.GrantedKeys)))
		if objectInventoryStale(b.ObservedAt, time.Duration(api.ObjectStorageInventoryMaxAgeSeconds)*time.Second, now) {
			stale[b.Bucket.ID] = true
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
	return u, stale
}

func objectInventoryStale(observedAt time.Time, maxAge time.Duration, now time.Time) bool {
	return observedAt.IsZero() || observedAt.After(now) || now.Sub(observedAt) > maxAge
}

func summarizeGatewaySafety(s ObjectUsageSnapshot, p api.ObjectStoragePolicy, now time.Time, u api.ObjectStorageUsage, stale map[string]bool) (api.ObjectStorageUsage, map[string]bool) {
	// Stored byte-hours are known once Gregale meters every live bucket
	// (ADR-20261024132844); provider cost never is in this mode.
	storageMetered := true
	for _, b := range s.Buckets {
		if b.Bucket.State == "deleted" && b.Bucket.UpdatedAt.Before(u.PeriodStart) {
			continue
		}
		u.StoredByteHours = boundedObjectAdd(u.StoredByteHours, b.StoredByteHours)
		if b.Bucket.State != "deleted" && b.StorageMeteredThrough.IsZero() {
			storageMetered = false
		}
		// Earlier buckets may have unmeasured direct traffic or old reusable
		// URLs. A configuration change must not retroactively bless that gap.
		if !b.GatewayMetricsKnown || p.GatewayMeteringSince == nil || p.GatewayMeteringSince.After(now) || b.Bucket.CreatedAt.IsZero() || b.Bucket.CreatedAt.Before(*p.GatewayMeteringSince) {
			u.Fresh = false
		}
		u.RequestCount = boundedObjectAdd(u.RequestCount, b.RequestCount)
		u.EgressBytes = boundedObjectAdd(u.EgressBytes, b.EgressBytes)
		if b.Bucket.State == "deleted" {
			continue
		}
		u.ObservedBytes = boundedObjectAdd(u.ObservedBytes, b.ObservedBytes)
		u.CapacityBytes = boundedObjectAdd(u.CapacityBytes, boundedObjectAdd(max(b.ObservedBytes, boundedObjectAdd(b.BaselineBytes, b.GrantedBytes)), b.MultipartBytes))
		u.CapacityKeys = boundedObjectAdd(u.CapacityKeys, max(b.ObservedKeys, boundedObjectAdd(b.BaselineKeys, b.GrantedKeys)))
		maxAge := time.Duration(min(p.MaxReportAgeSeconds, int64(api.ObjectStorageInventoryMaxAgeSeconds))) * time.Second
		if objectInventoryStale(b.ObservedAt, maxAge, now) {
			stale[b.Bucket.ID] = true
		}
	}
	u.UnavailableMeters = []string{"stored_byte_hours", "cost_millicents"}
	if storageMetered {
		u.UnavailableMeters = []string{"cost_millicents"}
	}
	return u, stale
}

// SummarizeObjectStorageBillingUsage reduces the authoritative provider
// reports for one UTC calendar month without applying the live admission
// policy's freshness window. Month-close runs after the period ends, so a
// report observed near the end of the month is valid even when it is older
// than the current admission window. It still requires one report for every
// backend that existed during the period and fails closed when the counters
// cannot be represented safely.
//
// A month Gregale metered itself (gateway-safety accounting, ADR-20261024132844)
// has no provider reports; it bills from the metered rows instead.
func SummarizeObjectStorageBillingUsage(s ObjectUsageSnapshot, periodStart time.Time) (api.ObjectStorageUsage, error) {
	periodStart = ObjectStoragePeriod(periodStart)
	for _, b := range s.Buckets {
		if !b.StorageMeteredThrough.IsZero() {
			if len(s.Reports) != 0 {
				return api.ObjectStorageUsage{}, ErrObjectBillingConflict
			}
			return summarizeGatewayStorageBilling(s, periodStart)
		}
	}
	u := api.ObjectStorageUsage{Fresh: true, PeriodStart: periodStart, Authorizations: s.Authorizations}
	periodEnd := periodStart.AddDate(0, 1, 0)
	required := map[string]string{}
	// A backend with a bucket still live at the month's end must report to
	// that end. Accepting any in-month report billed a month short by the
	// exporter's coverage lag, and a stalled exporter's last report locked in
	// an undercount (Hunt 19 H19-2). A backend whose buckets were all deleted
	// keeps its last report.
	liveAtEnd := map[string]bool{}
	for _, b := range s.Buckets {
		if !b.Bucket.CreatedAt.IsZero() && !b.Bucket.CreatedAt.Before(periodEnd) {
			continue
		}
		if b.Bucket.State == "deleted" && !b.Bucket.UpdatedAt.IsZero() && !b.Bucket.UpdatedAt.After(periodStart) {
			continue
		}
		if b.Bucket.BackendID != "" {
			required[b.Bucket.BackendID] = b.Bucket.BackendFingerprint
			if b.Bucket.State != "deleted" || !b.Bucket.UpdatedAt.Before(periodEnd) {
				liveAtEnd[b.Bucket.BackendID] = true
			}
		}
	}
	latest := map[string]api.ObjectStorageUsageReport{}
	for _, r := range s.Reports {
		if !r.PeriodStart.Equal(periodStart) {
			continue
		}
		if old, ok := latest[r.BackendID]; !ok || r.ObservedAt.After(old.ObservedAt) {
			latest[r.BackendID] = r
		}
	}
	for backendID, r := range latest {
		if fp, ok := required[backendID]; ok {
			if fp != r.BackendFingerprint {
				return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
			}
			if liveAtEnd[backendID] && r.ObservedAt.Before(periodEnd.Add(-api.ObjectStorageReportCloseTolerance)) {
				return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
			}
			delete(required, backendID)
		}
		for _, v := range []int64{r.StoredByteHours, r.RequestCount, r.EgressBytes, r.CostMillicents} {
			if v < 0 {
				return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
			}
		}
		u.StoredByteHours = boundedObjectAdd(u.StoredByteHours, r.StoredByteHours)
		u.RequestCount = boundedObjectAdd(u.RequestCount, r.RequestCount)
		u.EgressBytes = boundedObjectAdd(u.EgressBytes, r.EgressBytes)
		u.CostMillicents = boundedObjectAdd(u.CostMillicents, r.CostMillicents)
		if u.StoredByteHours > api.MaxObjectStoragePolicyValue || u.RequestCount > api.MaxObjectStoragePolicyValue || u.EgressBytes > api.MaxObjectStoragePolicyValue || u.CostMillicents > api.MaxObjectStoragePolicyValue {
			return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
		}
	}
	if len(required) != 0 {
		return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
	}
	return u, nil
}

func checkObjectAdmission(s ObjectUsageSnapshot, bucketID string, size, oldSize int64, exists, put bool, p api.ObjectStoragePolicy, now time.Time) (int64, int64, error) {
	p = s.planPolicy(p)
	u, stale := summarizeObjectUsage(s, p, now)
	// Only the target bucket's own inventory gates its admission; another
	// bucket's stale inventory is already covered by its conservative capacity.
	if !u.Fresh || stale[bucketID] {
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
		if p.GatewaySafety() && limit.name == "cost_millicents" {
			continue
		}
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
	bucketCapacity := boundedObjectAdd(max(found.ObservedBytes, boundedObjectAdd(found.BaselineBytes, found.GrantedBytes)), found.MultipartBytes)
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

// summarizeGatewayStorageBilling closes a gateway-metered month. Every metered
// row must be accrued to the end of the month, or to its bucket's deletion,
// before the month bills; until then it is incomplete and month close retries.
// A bucket with no row was never metered while it lived and bills nothing.
func summarizeGatewayStorageBilling(s ObjectUsageSnapshot, periodStart time.Time) (api.ObjectStorageUsage, error) {
	u := api.ObjectStorageUsage{Fresh: true, PeriodStart: periodStart, Authorizations: s.Authorizations, UnavailableMeters: []string{"cost_millicents"}}
	// Provider cost stays unknown; the charge prices metered storage only.
	periodEnd := periodStart.AddDate(0, 1, 0)
	for _, b := range s.Buckets {
		if !b.Bucket.CreatedAt.IsZero() && !b.Bucket.CreatedAt.Before(periodEnd) {
			continue
		}
		if b.Bucket.State == "deleted" && !b.Bucket.UpdatedAt.IsZero() && !b.Bucket.UpdatedAt.After(periodStart) {
			continue
		}
		u.RequestCount = boundedObjectAdd(u.RequestCount, b.RequestCount)
		u.EgressBytes = boundedObjectAdd(u.EgressBytes, b.EgressBytes)
		if b.StorageMeteredThrough.IsZero() {
			continue
		}
		closes := periodEnd
		if b.Bucket.State == "deleted" && b.Bucket.UpdatedAt.Before(periodEnd) {
			closes = b.Bucket.UpdatedAt
		}
		if b.StorageMeteredThrough.Before(closes) {
			return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
		}
		u.StoredByteHours = boundedObjectAdd(u.StoredByteHours, b.StoredByteHours)
	}
	if u.StoredByteHours > api.MaxObjectStoragePolicyValue || u.RequestCount > api.MaxObjectStoragePolicyValue || u.EgressBytes > api.MaxObjectStoragePolicyValue {
		return api.ObjectStorageUsage{}, ErrObjectBillingIncomplete
	}
	return u, nil
}
