package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// dataAPIDiagnosticFixture renders one data_api_request log line. Internal
// tests call it directly; main_test reaches it through export_test.go
// (ADR-982), so both test units share one definition.
func dataAPIDiagnosticFixture(t *testing.T, id int, route, outcome string, status any, duration float64, code any) string {
	t.Helper()
	content, err := json.Marshal(map[string]any{"time": "2026-10-11T10:00:00Z", "level": "info", "event": "data_api_request", "request_id": fmt.Sprintf("00000000-0000-4000-8000-%012d", id), "method": "GET", "route": route, "status": status, "duration_ms": duration, "outcome": outcome, "code": code})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
