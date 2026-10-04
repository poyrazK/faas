package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func previewSecurityDocument(t *testing.T, requirements any, name string) string {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(previewRequestDocument(t, map[string]any{"type": "string"})), &doc); err != nil {
		t.Fatal(err)
	}
	if requirements != nil {
		doc["security"] = requirements
	}
	if name == "" {
		name = "private-header"
	}
	doc["components"] = map[string]any{"securitySchemes": map[string]any{"private-token": map[string]any{"type": "apiKey", "in": "header", "name": name, "description": "private-description"}}}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestPreviewSecurityCommandGates(t *testing.T) {
	protected := []any{map[string]any{"private-token": []any{}}}
	optional := []any{map[string]any{"private-token": []any{}}, map[string]any{}}
	for _, test := range []struct {
		name, gate, outcome, status, header string
		before, after                       any
		exit                                int
	}{
		{"regression default", "", "security_regressions", "regression", "", protected, optional, 0},
		{"regression gate", "--fail-on-security-regression", "security_regressions", "regression", "", protected, []any{}, 1},
		{"regression response gate", "--fail-on-breaking", "security_regressions", "regression", "", protected, []any{}, 0},
		{"regression request gate", "--fail-on-request-breaking", "security_regressions", "regression", "", protected, []any{}, 0},
		{"regression incomplete gate", "--fail-on-incomplete", "security_regressions", "regression", "", protected, []any{}, 1},
		{"client break default", "", "security_client_breaking_changes", "client_breaking", "", nil, protected, 0},
		{"client break request gate", "--fail-on-request-breaking", "security_client_breaking_changes", "client_breaking", "", nil, protected, 1},
		{"client break security gate", "--fail-on-security-regression", "security_client_breaking_changes", "client_breaking", "", nil, protected, 0},
		{"unknown transport", "--fail-on-security-regression", "incomplete", "unknown", "private-renamed", protected, protected, 0},
		{"unknown strict gate", "--fail-on-incomplete", "incomplete", "unknown", "private-renamed", protected, protected, 1},
		{"unchanged", "--fail-on-security-regression", "no_findings", "unchanged", "", protected, protected, 0},
		{"malformed", "--fail-on-security-regression", "incomplete", "unknown", "", protected, []any{nil}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			reads := servePreviewRequestReport(t, previewSecurityDocument(t, test.before, ""), previewSecurityDocument(t, test.after, test.header), true)
			var output bytes.Buffer
			old := osStdout
			osStdout = &output
			t.Cleanup(func() { osStdout = old })
			jsonOutput = true
			args := []string{"report", "pr-42-api", "--test-report", previewRequestReceipts(t)}
			if test.gate != "" {
				args = append(args, test.gate)
			}
			if exit := cmdPreview(args); exit != test.exit {
				t.Fatalf("exit %d want %d: %s", exit, test.exit, output.String())
			}
			var report previewRouteReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			row := findPreviewReportRoute(t, report, "POST /checkout")
			if report.Version != 6 || report.Security.Status != "available" || report.Outcome != test.outcome || row.SecurityCompatibility == nil || row.SecurityCompatibility.Status != test.status || row.RequestCompatibility.Status != "no_supported_breaks" || row.RequestContractChanged || reads.Load() != 8 {
				t.Fatalf("report %+v; row %+v; reads %d", report, row, reads.Load())
			}
			if strings.Contains(output.String(), "private") {
				t.Fatalf("security metadata leaked: %s", output.String())
			}
			if test.status == "regression" || test.status == "client_breaking" {
				if len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "blocker" || report.ReviewPriorities[0].CandidateChecks != "passed_samples" {
					t.Fatalf("passing tests hid security change: %+v", report.ReviewPriorities)
				}
			} else if test.status == "unknown" {
				if len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "needs_evidence" {
					t.Fatal(report.ReviewPriorities)
				}
			} else if len(report.ReviewPriorities) != 0 {
				t.Fatal(report.ReviewPriorities)
			}
			for _, markdown := range []bool{false, true} {
				var rendered bytes.Buffer
				renderPreviewRouteReport(&rendered, report, markdown)
				if strings.Contains(rendered.String(), "private") || !strings.Contains(rendered.String(), "Declared authentication changes") {
					t.Fatalf("render: %s", rendered.String())
				}
				if test.status == "regression" && !strings.Contains(rendered.String(), "declared anonymous") {
					t.Fatal(rendered.String())
				}
			}
		})
	}
}

func TestPreviewSecurityUnavailableAndOutcomePrecedence(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	servePreviewRequestReport(t, previewSecurityDocument(t, nil, ""), previewSecurityDocument(t, nil, ""), false)
	var output bytes.Buffer
	old := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = old })
	jsonOutput = true
	if exit := cmdPreview([]string{"report", "pr-42-api", "--fail-on-security-regression"}); exit != 0 {
		t.Fatalf("unknown failed regression gate: %s", output.String())
	}
	var report previewRouteReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Security.Status != "unavailable" {
		t.Fatal(report)
	}
	foundRequest, foundSecurity := false, false
	for _, review := range report.ReviewPriorities {
		foundRequest = foundRequest || containsPreviewRequestReason(review.Reasons, "request_comparison_unavailable")
		if containsPreviewRequestReason(review.Reasons, "security_comparison_unavailable") {
			foundSecurity = true
		}
	}
	if !foundRequest || !foundSecurity {
		t.Fatal(report.ReviewPriorities)
	}
	for _, outcome := range []string{"breaking_changes", "request_breaking_changes", "test_failures", "policy_violations"} {
		report := previewRouteReport{Outcome: outcome, Security: previewReportEvidence{Status: "available"}, Routes: []previewReportRoute{{SecurityCompatibility: &openapidiff.SecurityRoute{Status: "regression", Regression: true, Complete: true}}}}
		finishPreviewSecurityComparison(&report)
		if report.Outcome != outcome || !previewReportHasSecurityRegressions(report) {
			t.Fatalf("security hid existing finding: %+v", report)
		}
	}
}

func TestPreviewSecurityReviewOrderingAndKnownUnknown(t *testing.T) {
	report := previewRouteReport{Outcome: "no_findings", Security: previewReportEvidence{Status: "available"}, Routes: []previewReportRoute{
		{Method: "GET", Path: "/a-quiet", RouteSource: "captured_deployment_contract", SecurityCompatibility: &openapidiff.SecurityRoute{Status: "regression", Regression: true, Complete: false}, TestProfiles: []previewReportTest{{Passed: 1}}, BaselineTraffic: &previewReportTraffic{Requests: 10}},
		{Method: "GET", Path: "/z-busy", RouteSource: "captured_deployment_contract", SecurityCompatibility: &openapidiff.SecurityRoute{Status: "client_breaking", ClientBreaking: true, Complete: true}, TestProfiles: []previewReportTest{{Passed: 1}}, BaselineTraffic: &previewReportTraffic{Requests: 100}},
	}}
	finishPreviewSecurityComparison(&report)
	prioritizePreviewRouteReview(&report)
	if report.Outcome != "security_regressions" || len(report.ReviewPriorities) != 2 || report.ReviewPriorities[0].Path != "/z-busy" || report.ReviewPriorities[0].BaselineTraffic == nil || report.ReviewPriorities[0].BaselineTraffic.Requests != 100 || report.ReviewPriorities[1].Priority != "blocker" || !containsPreviewRequestReason(report.ReviewPriorities[1].Reasons, "security_comparison_unknown") {
		t.Fatal(report)
	}
}
