package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestObjectStorageCustomerUsageV2JSON(t *testing.T) {
	// adr: 237 — unknown egress is explicit null, never a fabricated zero.
	report := ObjectStorageCustomerUsageReportV2{
		AccountID: "account", BackendID: "backend", BackendFingerprint: strings.Repeat("a", 64),
		Source: "provider-metrics", Version: ObjectStorageCustomerUsageVersion,
		PeriodStart:    time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		CoverageEnd:    time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC),
		ObservedAt:     time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC),
		EvidenceDigest: strings.Repeat("b", 64), ReadOperations: 2,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"egress_bytes":null`) || strings.Contains(string(encoded), "cost_millicents") {
		t.Fatal(string(encoded))
	}
	var decoded ObjectStorageCustomerUsageReportV2
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.EgressBytes != nil || decoded.ReadOperations != 2 {
		t.Fatalf("decode: %+v, %v", decoded, err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"read_operations", "egress_bytes", "version"} {
		copy := make(map[string]any, len(fields))
		for k, v := range fields {
			copy[k] = v
		}
		delete(copy, key)
		data, _ := json.Marshal(copy)
		if err := json.Unmarshal(data, &decoded); err == nil {
			t.Fatalf("accepted missing %s", key)
		}
	}
	fields["cost_millicents"] = 0
	data, _ := json.Marshal(fields)
	if err := json.Unmarshal(data, &decoded); err == nil {
		t.Fatal("accepted provider cost in customer meter")
	}
	duplicate := strings.Replace(string(encoded), `"read_operations":2`, `"read_operations":2,"read_operations":3`, 1)
	if err := json.Unmarshal([]byte(duplicate), &decoded); err == nil {
		t.Fatal("accepted duplicate usage field")
	}
}
