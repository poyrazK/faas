package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const ObjectStorageCustomerUsageVersion = 2

// ObjectStorageCustomerUsageReportV2 is shadow-only, provider-normalized
// customer meter evidence. It deliberately has no provider-cost field. A nil
// EgressBytes means unknown, not zero; a charged egress meter cannot use it.
// Legacy ObjectStorageUsageReport remains the live admission/billing source.
type ObjectStorageCustomerUsageReportV2 struct {
	AccountID          string    `json:"account_id"`
	BackendID          string    `json:"backend_id"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	Source             string    `json:"source"`
	Version            int       `json:"version"`
	PeriodStart        time.Time `json:"period_start"`
	CoverageEnd        time.Time `json:"coverage_end"`
	ObservedAt         time.Time `json:"observed_at"`
	EvidenceDigest     string    `json:"evidence_digest"`
	StoredByteHours    int64     `json:"stored_byte_hours"`
	ReadOperations     int64     `json:"read_operations"`
	WriteOperations    int64     `json:"write_operations"`
	EgressBytes        *int64    `json:"egress_bytes"`
}

// UnmarshalJSON rejects absent/null required fields and unknown fields. The
// egress field is required but may explicitly be null to record uncertainty.
func (r *ObjectStorageCustomerUsageReportV2) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return errors.New("invalid object storage v2 usage object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return errors.New("invalid object storage v2 usage field")
		}
		if _, duplicate := fields[key]; duplicate {
			return errors.New("duplicate object storage v2 usage field")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing object storage v2 usage data")
	}
	required := []string{"account_id", "backend_id", "backend_fingerprint", "source", "version", "period_start", "coverage_end", "observed_at", "evidence_digest", "stored_byte_hours", "read_operations", "write_operations", "egress_bytes"}
	if len(fields) != len(required) {
		return errors.New("incomplete or unknown object storage v2 usage fields")
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || (key != "egress_bytes" && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return errors.New("missing object storage v2 usage measurement")
		}
	}
	type plain ObjectStorageCustomerUsageReportV2
	return json.Unmarshal(data, (*plain)(r))
}
