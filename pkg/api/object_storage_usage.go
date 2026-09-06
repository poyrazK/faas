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

// ObjectStoragePricing is an operator-supplied customer rate card. Rates are
// integer millicents (1000 millicents = 1 cent) and are deliberately separate
// from ObjectStoragePolicy, which contains safety ceilings rather than prices.
// A nil pricing configuration means that Gregale reports upstream usage/cost
// only and does not estimate a customer charge.
type ObjectStoragePricing struct {
	Currency                     string `json:"currency"`
	StorageMillicentsPerGiBMonth int64  `json:"storage_millicents_per_gib_month"`
	RequestsMillicentsPerMillion int64  `json:"requests_millicents_per_million"`
	EgressMillicentsPerGiB       int64  `json:"egress_millicents_per_gib"`
}

func (p ObjectStoragePricing) Valid() bool {
	if len(p.Currency) != 3 {
		return false
	}
	for i := 0; i < len(p.Currency); i++ {
		if p.Currency[i] < 'A' || p.Currency[i] > 'Z' {
			return false
		}
	}
	for _, v := range []int64{p.StorageMillicentsPerGiBMonth, p.RequestsMillicentsPerMillion, p.EgressMillicentsPerGiB} {
		if v < 0 || v > MaxObjectStoragePolicyValue {
			return false
		}
	}
	return true
}

// ObjectStorageCharge is the deterministic estimate for the current UTC
// month. Components are rounded up independently to one millicent so a
// configured non-zero rate cannot be under-collected by fractional units.
// It is an estimate until a billing-provider adapter posts the corresponding
// line item; it is not itself an invoice or a payment authorization.
type ObjectStorageCharge struct {
	Currency           string `json:"currency"`
	StorageMillicents  int64  `json:"storage_millicents"`
	RequestsMillicents int64  `json:"requests_millicents"`
	EgressMillicents   int64  `json:"egress_millicents"`
	TotalMillicents    int64  `json:"total_millicents"`
}

type ObjectStorageUsageResponse struct {
	Usage   ObjectStorageUsage   `json:"usage"`
	Policy  ObjectStoragePolicy  `json:"policy"`
	Charges *ObjectStorageCharge `json:"charges,omitempty"`
}
