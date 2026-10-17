package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestDataAPIDiagnosticsQueryShapesAggregateAndRejectUntrustedValues(t *testing.T) {
	collector := newDataAPIDiagnosticsCollector("", "")
	shape := dataAPIQueryShape{Projection: []string{"body", "id"}, Filters: []dataAPIShapeFilter{{Column: "body", Operator: "eq"}}, Order: []dataAPIShapeOrder{{Column: "id", Ascending: true}}, Count: "none"}
	raw, _ := json.Marshal([]any{"GET", "api.notes", "relation", shape})
	id := fmt.Sprintf("%x", sha256.Sum256(raw))
	for i := 1; i <= 3; i++ {
		var record map[string]any
		_ = json.Unmarshal([]byte(dataAPIDiagnosticFixture(t, i, "rest", "completed", 200, float64(i*10), nil)), &record)
		record["operation"] = "api.notes"
		record["operation_kind"] = "relation"
		record["query_shape_id"] = id
		record["query_shape"] = shape
		record["schema_fingerprint"] = strings.Repeat("a", 64)
		record["raw_url"] = "private-filter-value"
		record["timings"] = map[string]any{"upstream": map[string]any{"duration_ms": i, "complete": true}}
		raw, _ := json.Marshal(record)
		collector.observe(string(raw))
	}
	collector.observe(dataAPIDiagnosticFixture(t, 4, "rest", "authentication_failed", 401, 2, "token_invalid"))
	collector.finish()
	report := collector.report
	if report.ShapedRequests != 3 || report.UnshapedDataRequests != 1 || len(report.QueryShapes) != 1 || report.QueryShapes[0].Stats.Requests != 3 || len(report.QueryShapes[0].RequestIDs) != 3 {
		t.Fatalf("wrong shape stats: %+v", report)
	}
	if len(report.Operations) != 1 || report.Operations[0].Stats.Requests != 3 || report.Operations[0].ShapeCount != 1 {
		t.Fatal("wrong operation aggregates")
	}
	if *report.QueryShapes[0].Stats.LatencyMS.P95 != 30 {
		t.Fatal("wrong per-shape percentiles")
	}
	if *report.QueryShapes[0].Stats.Timings.ByPhase["upstream"].LatencyMS.P95 != 3 || report.Operations[0].Stats.Timings.ObservedRequests != 3 {
		t.Fatal("phase timings missing from operation/shape stats")
	}
	raw, _ = json.Marshal(report)
	if strings.Contains(string(raw), "private-filter-value") {
		t.Fatal("unknown raw query field leaked")
	}
	var record map[string]any
	_ = json.Unmarshal([]byte(dataAPIDiagnosticFixture(t, 5, "rest", "completed", 200, 1, nil)), &record)
	record["operation"] = "api.notes"
	record["operation_kind"] = "relation"
	record["query_shape_id"] = id
	record["schema_fingerprint"] = strings.Repeat("a", 64)
	unsafe := shape
	unsafe.Projection = []string{"body=eq.private-filter-value"}
	record["query_shape"] = unsafe
	raw, _ = json.Marshal(record)
	if _, _, err := parseDataAPIRequestRecord(string(raw)); err == nil {
		t.Fatal("untrusted shape values accepted")
	}
	record["query_shape"] = shape
	record["query_shape_id"] = strings.Repeat("0", 64)
	raw, _ = json.Marshal(record)
	if _, _, err := parseDataAPIRequestRecord(string(raw)); err == nil {
		t.Fatal("tampered shape digest accepted")
	}
}
