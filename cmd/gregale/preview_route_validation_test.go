package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func previewNullableValidationSchema(value any) map[string]any {
	return map[string]any{"anyOf": []any{value, map[string]any{"type": "null"}}, "default": nil, "title": "Quantity"}
}

func TestPreviewValidationCompatibilityGates(t *testing.T) {
	for _, test := range []struct {
		name, gate, outcome, status, finding string
		before, after                        any
		code                                 int
	}{
		{"numeric restriction", "--fail-on-request-breaking", "request_breaking_changes", "breaking", "numeric_maximum_restricted", map[string]any{"type": "integer", "minimum": 1, "maximum": 100}, map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, 1},
		{"length restriction", "--fail-on-request-breaking", "request_breaking_changes", "breaking", "string_max_length_restricted", map[string]any{"type": "string", "maxLength": 100}, map[string]any{"type": "string", "maxLength": 20}, 1},
		{"array restriction", "--fail-on-request-breaking", "request_breaking_changes", "breaking", "array_min_items_restricted", map[string]any{"type": "array"}, map[string]any{"type": "array", "minItems": 1}, 1},
		{"nullable restriction", "--fail-on-request-breaking", "request_breaking_changes", "breaking", "numeric_maximum_restricted", previewNullableValidationSchema(map[string]any{"type": "integer", "maximum": 100}), previewNullableValidationSchema(map[string]any{"type": "integer", "maximum": 20}), 1},
		{"nullable widening", "--fail-on-incomplete", "no_findings", "no_supported_breaks", "", previewNullableValidationSchema(map[string]any{"type": "integer", "maximum": 20}), previewNullableValidationSchema(map[string]any{"type": "integer", "maximum": 100}), 0},
		{"integer equivalent", "--fail-on-incomplete", "no_findings", "no_supported_breaks", "", map[string]any{"type": "integer", "exclusiveMinimum": 0}, map[string]any{"type": "integer", "minimum": 1}, 0},
		{"integer range enum equivalent", "--fail-on-incomplete", "no_findings", "no_supported_breaks", "", map[string]any{"type": "integer", "minimum": 1, "maximum": 2}, map[string]any{"type": "integer", "enum": []any{1, 2}}, 0},
		{"independent response gate", "--fail-on-breaking", "request_breaking_changes", "breaking", "numeric_maximum_restricted", map[string]any{"type": "integer", "maximum": 100}, map[string]any{"type": "integer", "maximum": 20}, 0},
		{"invalid bound", "--fail-on-incomplete", "incomplete", "unknown", "invalid_numeric_bound", map[string]any{"type": "integer"}, map[string]any{"type": "integer", "minimum": "private-value"}, 1},
		{"ambiguous union", "--fail-on-request-breaking", "incomplete", "unknown", "nullable_union_not_compared", map[string]any{"type": "integer"}, map[string]any{"anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			// Mirror a generated body model with a nullable/constrained field.
			model := func(field any) map[string]any {
				return map[string]any{"type": "object", "properties": map[string]any{"quantity": field}, "required": []any{"quantity"}}
			}
			reads := servePreviewRequestReport(t, previewRequestDocument(t, model(test.before)), previewRequestDocument(t, model(test.after)), true)
			var out bytes.Buffer
			previous := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = previous })
			jsonOutput = true
			if code := cmdPreview([]string{"report", "pr-42-api", "--test-report", previewRequestReceipts(t), test.gate}); code != test.code {
				t.Fatalf("exit = %d, want %d: %s", code, test.code, out.String())
			}
			var report previewRouteReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			row := findPreviewReportRoute(t, report, "POST /checkout")
			if report.Version != 6 || report.Outcome != test.outcome || report.Requests.Status != "available" || row.RequestCompatibility == nil || row.RequestCompatibility.Status != test.status || len(row.Breaks) != 0 || reads.Load() != 8 {
				t.Fatalf("report = %+v, route = %+v", report, row)
			}
			found := test.finding == ""
			for _, finding := range row.RequestCompatibility.Findings {
				found = found || finding.Code == test.finding
			}
			if !found || strings.Contains(out.String(), "private-") {
				t.Fatalf("finding missing or private value leaked: %s", out.String())
			}
			if test.status == "breaking" && (len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "blocker" || report.ReviewPriorities[0].CandidateChecks != "passed_samples") {
				t.Fatalf("passing samples erased a validation restriction: %+v", report.ReviewPriorities)
			}
			if test.outcome == "no_findings" && len(report.ReviewPriorities) != 0 {
				t.Fatal(report.ReviewPriorities)
			}
		})
	}
}

func TestPreviewValidationFindingsRenderWithoutLimits(t *testing.T) {
	before := previewReportSpec(t, previewRequestDocument(t, map[string]any{"type": "number", "maximum": 123456789}))
	after := previewReportSpec(t, previewRequestDocument(t, map[string]any{"type": "number", "maximum": 9876543, "example": "private-example"}))
	rows, requests := comparePreviewContractsWithRequests(before, after)
	report := previewRouteReport{Requests: requests, Routes: rows, Outcome: "request_breaking_changes"}
	prioritizePreviewRouteReview(&report)
	for _, markdown := range []bool{false, true} {
		var out bytes.Buffer
		renderPreviewRouteReport(&out, report, markdown)
		if !strings.Contains(out.String(), "maximum accepted numeric input became stricter") || strings.Contains(out.String(), "123456789") || strings.Contains(out.String(), "9876543") || strings.Contains(out.String(), "private-") {
			t.Fatalf("incomplete or unredacted validation finding: %s", out.String())
		}
	}
}
