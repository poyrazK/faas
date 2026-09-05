package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// ObjectStoragePolicy is an operator safety budget, not a plan price or a
// promise that already-issued capabilities can be revoked.
type ObjectStoragePolicy struct {
	MaxAccountBytes          int64 `json:"max_account_bytes"`
	MaxBucketBytes           int64 `json:"max_bucket_bytes"`
	MaxAccountKeys           int64 `json:"max_account_keys"`
	MaxMonthlyCostMillicents int64 `json:"max_monthly_cost_millicents"`
	MaxMonthlyRequests       int64 `json:"max_monthly_requests"`
	MaxMonthlyEgressBytes    int64 `json:"max_monthly_egress_bytes"`
	MaxMonthlyAuthorizations int64 `json:"max_monthly_authorizations"`
	MaxReportAgeSeconds      int64 `json:"max_report_age_seconds"`
}

func (p ObjectStoragePolicy) Valid() bool {
	for _, v := range []int64{p.MaxAccountBytes, p.MaxBucketBytes, p.MaxAccountKeys, p.MaxMonthlyCostMillicents, p.MaxMonthlyRequests, p.MaxMonthlyEgressBytes, p.MaxMonthlyAuthorizations} {
		if v < 1 || v > MaxObjectStoragePolicyValue {
			return false
		}
	}
	return p.MaxAccountKeys <= ObjectStorageInventoryMaxPages*1000 && p.MaxBucketBytes <= p.MaxAccountBytes && p.MaxReportAgeSeconds >= 60 && p.MaxReportAgeSeconds <= MaxObjectStorageReportAgeSeconds
}

// ObjectStorageUsageReport is an authoritative cumulative UTC-month report
// from an operator's provider adapter. Costs are EUR millicents (not invoices).
// Counters must include actual provider traffic, not URL issuance counts.
type ObjectStorageUsageReport struct {
	AccountID          string    `json:"account_id"`
	BackendID          string    `json:"backend_id"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	Source             string    `json:"source"`
	PeriodStart        time.Time `json:"period_start"`
	ObservedAt         time.Time `json:"observed_at"`
	StoredByteHours    int64     `json:"stored_byte_hours"`
	RequestCount       int64     `json:"request_count"`
	EgressBytes        int64     `json:"egress_bytes"`
	CostMillicents     int64     `json:"cost_millicents"`
}

// UnmarshalJSON distinguishes an explicit zero measurement from missing or
// null data. Custom decoding must also reject unknown fields itself.
func (r *ObjectStorageUsageReport) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	required := []string{"account_id", "backend_id", "backend_fingerprint", "source", "period_start", "observed_at", "stored_byte_hours", "request_count", "egress_bytes", "cost_millicents"}
	if len(fields) != len(required) {
		return errors.New("incomplete or unknown object storage usage fields")
	}
	for _, key := range required {
		if value, ok := fields[key]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("missing object storage usage measurement")
		}
	}
	type plain ObjectStorageUsageReport
	return json.Unmarshal(data, (*plain)(r))
}

type ObjectStorageUsage struct {
	ObservedBytes   int64     `json:"observed_bytes"`
	CapacityBytes   int64     `json:"capacity_bytes"`
	CapacityKeys    int64     `json:"capacity_keys"`
	StoredByteHours int64     `json:"stored_byte_hours"`
	RequestCount    int64     `json:"request_count"`
	EgressBytes     int64     `json:"egress_bytes"`
	CostMillicents  int64     `json:"cost_millicents"`
	Authorizations  int64     `json:"authorizations"`
	Fresh           bool      `json:"fresh"`
	PeriodStart     time.Time `json:"period_start"`
}

type ObjectStorageUsageResponse struct {
	Usage  ObjectStorageUsage  `json:"usage"`
	Policy ObjectStoragePolicy `json:"policy"`
}
