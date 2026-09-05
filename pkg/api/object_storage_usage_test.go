package api

import (
	"encoding/json"
	"testing"
)

func TestObjectStorageReportRequiresEveryMeasurement(t *testing.T) {
	valid := `{"account_id":"a","backend_id":"b","backend_fingerprint":"f","source":"provider","period_start":"2026-09-01T00:00:00Z","observed_at":"2026-09-05T00:00:00Z","stored_byte_hours":0,"request_count":0,"egress_bytes":0,"cost_millicents":0}`
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(valid), &fields); err != nil {
		t.Fatal(err)
	}
	for key := range fields {
		original := fields[key]
		delete(fields, key)
		data, _ := json.Marshal(fields)
		var report ObjectStorageUsageReport
		if json.Unmarshal(data, &report) == nil {
			t.Errorf("missing %s accepted", key)
		}
		fields[key] = json.RawMessage("null")
		data, _ = json.Marshal(fields)
		if json.Unmarshal(data, &report) == nil {
			t.Errorf("null %s accepted", key)
		}
		fields[key] = original
	}
	var report ObjectStorageUsageReport
	if err := json.Unmarshal([]byte(valid), &report); err != nil {
		t.Fatal("explicit zero measurements rejected", err)
	}
}
