package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/routerequirements"
)

func previewRequestDocument(t *testing.T, schema any) string {
	t.Helper()
	doc := map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "checkout", "version": "1"},
		"paths": map[string]any{"/checkout": map[string]any{"post": map[string]any{
			"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": schema}}},
			"responses":   map[string]any{"200": map[string]any{"description": "ok", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "string"}}}}},
		}}}}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func servePreviewRequestReport(t *testing.T, before, after string, captured bool) *atomic.Int32 {
	t.Helper()
	reads := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet {
			t.Errorf("request comparison mutated state: %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			writeJSONTest(w, api.PreviewResourceResponse{
				App: api.AppResponse{ID: "preview-id", Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{ID: "parent-id", Slug: "api"},
				LatestDeployment: &api.DeploymentResponse{ID: "candidate", AppID: "preview-id", Status: "live"}, ProductionDeployment: &api.DeploymentResponse{ID: "baseline", AppID: "parent-id", Status: "live"},
			})
		case "/v1/apps/api/deployments/baseline/openapi":
			if !captured {
				http.NotFound(w, r)
				return
			}
			writePreviewReportDoc(t, w, "baseline", before)
		case "/v1/apps/pr-42-api/deployments/candidate/openapi":
			writePreviewReportDoc(t, w, "candidate", after)
		case "/v1/apps/pr-42-api/openapi/preview":
			writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{Routes: []api.AppOpenAPIPolicyPreviewRoute{{Path: "/checkout", Method: "post", Rules: []api.AppOpenAPIPolicyPreviewRule{}}}})
		case "/v1/apps/api/analytics", "/v1/apps/pr-42-api/analytics":
			id := "baseline"
			if strings.Contains(r.URL.Path, "pr-42-api") {
				id = "candidate"
			}
			data := previewReportAnalytics(id, 100, 40)
			data.Routes[0].Route, data.Routes[0].Method = "POST /checkout", "POST"
			writeJSONTest(w, data)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	return reads
}

func previewRequestReceipts(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "receipts.json")
	start := time.Now().UTC()
	receipts := []testRunReceipt{{Engine: "real-vm", AppSlug: "pr-42-api", DeploymentID: "candidate", Profile: "warm", Status: "passed", StartedAt: start, FinishedAt: start.Add(time.Second),
		Requests: []testHTTPRequestEvidence{{Method: "POST", Path: "/checkout?token=private-query", Passed: true}}}}
	if err := writeTestReport(path, receipts); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPreviewRequestCompatibilityCommandGates(t *testing.T) {
	field := map[string]any{"type": "string", "enum": []any{"private-eur", "private-usd"}, "example": "private-example"}
	optional := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"currency": field}}
	required := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"currency": field}, "required": []any{"currency"}}
	for _, test := range []struct {
		name, gate, outcome, status string
		before, after               any
		code                        int
	}{
		{"restriction default", "", "request_breaking_changes", "breaking", optional, required, 0},
		{"restriction request gate", "--fail-on-request-breaking", "request_breaking_changes", "breaking", optional, required, 1},
		{"restriction response gate", "--fail-on-breaking", "request_breaking_changes", "breaking", optional, required, 0},
		{"restriction incomplete gate", "--fail-on-incomplete", "request_breaking_changes", "breaking", optional, required, 1},
		{"widening request gate", "--fail-on-request-breaking", "no_findings", "no_supported_breaks", required, optional, 0},
		{"widening incomplete gate", "--fail-on-incomplete", "no_findings", "no_supported_breaks", required, optional, 0},
		{"unknown request gate", "--fail-on-request-breaking", "incomplete", "unknown", map[string]any{"type": "string"}, map[string]any{"type": "string", "pattern": "private-pattern"}, 0},
		{"unknown incomplete gate", "--fail-on-incomplete", "incomplete", "unknown", map[string]any{"type": "string"}, map[string]any{"type": "string", "pattern": "private-pattern"}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetJSONOut(t)
			setPreviewTestAuth(t)
			reads := servePreviewRequestReport(t, previewRequestDocument(t, test.before), previewRequestDocument(t, test.after), true)
			var out bytes.Buffer
			old := osStdout
			osStdout = &out
			t.Cleanup(func() { osStdout = old })
			jsonOutput = true
			args := []string{"report", "pr-42-api", "--test-report", previewRequestReceipts(t)}
			if test.gate != "" {
				args = append(args, test.gate)
			}
			if code := cmdPreview(args); code != test.code {
				t.Fatalf("exit = %d, want %d; %s", code, test.code, out.String())
			}
			var report previewRouteReport
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			row := findPreviewReportRoute(t, report, "POST /checkout")
			if report.Version != 4 || report.SourceImpact != nil || report.Outcome != test.outcome || report.Requests.Status != "available" || row.RequestCompatibility == nil || row.RequestCompatibility.Status != test.status || len(row.Breaks) != 0 || !row.RequestContractChanged {
				t.Fatalf("report = %+v; route = %+v", report, row)
			}
			if reads.Load() != 6 {
				t.Fatalf("reads = %d, want 6", reads.Load())
			}
			if strings.Contains(out.String(), "private-") {
				t.Fatalf("request values, examples, or queries leaked: %s", out.String())
			}
			if test.status == "breaking" && (len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "blocker" || report.ReviewPriorities[0].CandidateChecks != "passed_samples") {
				t.Fatalf("passing samples hid a request break: %+v", report.ReviewPriorities)
			}
			if test.outcome == "no_findings" && len(report.ReviewPriorities) != 0 {
				t.Fatalf("supported widening requires no request review: %+v", report.ReviewPriorities)
			}
			if test.status == "unknown" && (len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "needs_evidence") {
				t.Fatal(report.ReviewPriorities)
			}
		})
	}
}

func TestPreviewRequestComparisonNeedsCapturedDocuments(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	doc := previewRequestDocument(t, map[string]any{"type": "string"})
	servePreviewRequestReport(t, doc, doc, false)
	var out bytes.Buffer
	old := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = old })
	jsonOutput = true
	if code := cmdPreview([]string{"report", "pr-42-api", "--test-report", previewRequestReceipts(t), "--fail-on-request-breaking"}); code != 0 {
		t.Fatalf("missing evidence triggered a known-break gate: %s", out.String())
	}
	var report previewRouteReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	row := findPreviewReportRoute(t, report, "POST /checkout")
	if report.Outcome != "incomplete" || report.Requests.Status != "unavailable" || row.RequestCompatibility != nil || row.Change != "unknown" || row.RouteSource == "captured_deployment_contract" {
		t.Fatalf("current route established request compatibility: %+v", report)
	}
}

func TestPreviewRequestComparisonLimitDiscardsPartialFindings(t *testing.T) {
	before := previewReportSpec(t, previewReportBefore)
	after := previewReportSpec(t, previewReportAfter)
	for index := 0; index <= api.RequestCompatibilityMaxRoutes; index++ {
		path := fmt.Sprintf("/limit/%d", index)
		item := &openapidiff.PathItem{Methods: map[string]*openapidiff.Operation{"get": {Raw: map[string]any{}}}}
		before.Paths[path], after.Paths[path] = item, item
	}
	rows, evidence := comparePreviewContractsWithRequests(before, after)
	report := previewRouteReport{Requests: evidence, Routes: rows}
	if evidence.Status != "unavailable" || previewReportHasRequestBreaks(report) || !previewReportHasBreaks(report) {
		t.Fatalf("bounded comparison certified partial inputs or lost response breaks: %+v", evidence)
	}
	for _, row := range rows {
		if row.Change != "added" && row.Change != "removed" && (row.RequestCompatibility == nil || row.RequestCompatibility.Complete || row.RequestCompatibility.Status != "unknown" || len(row.RequestCompatibility.Findings) != 1 || row.RequestCompatibility.Findings[0].Code != "comparison_unavailable") {
			t.Fatal(row)
		}
	}
}

func TestPreviewRequestOutcomePreservesOtherKnownFindings(t *testing.T) {
	for _, outcome := range []string{"breaking_changes", "test_failures", "policy_violations"} {
		report := previewRouteReport{Outcome: outcome, Requests: previewReportEvidence{Status: "available"}, Routes: []previewReportRoute{{RequestCompatibility: &openapidiff.RequestRoute{Status: "breaking"}}}}
		finishPreviewRequestComparison(&report)
		if report.Outcome != outcome {
			t.Fatalf("request comparison hid %s: %s", outcome, report.Outcome)
		}
	}
	report := previewRouteReport{Outcome: "no_findings", Requests: previewReportEvidence{Status: "available"}, Routes: []previewReportRoute{{Method: "POST", Path: "/checkout", RouteSource: "captured_deployment_contract", RequestCompatibility: &openapidiff.RequestRoute{Status: "breaking", Complete: false}, TestProfiles: []previewReportTest{{Passed: 1}}}}}
	finishPreviewRequestComparison(&report)
	prioritizePreviewRouteReview(&report)
	if report.Outcome != "request_breaking_changes" || len(report.ReviewPriorities) != 1 || report.ReviewPriorities[0].Priority != "blocker" || !containsPreviewRequestReason(report.ReviewPriorities[0].Reasons, "request_comparison_unknown") {
		t.Fatal(report)
	}
}

func containsPreviewRequestReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if reason == want {
			return true
		}
	}
	return false
}

func TestPreviewRequestReviewPrioritiesAndSourceEvidence(t *testing.T) {
	row := func(path, status string, complete bool, count int64) previewReportRoute {
		return previewReportRoute{Method: "POST", Path: path, RouteSource: "captured_deployment_contract", RequestCompatibility: &openapidiff.RequestRoute{Status: status, Complete: complete}, BaselineTraffic: &previewReportTraffic{Requests: count}, TestProfiles: []previewReportTest{{Passed: 1}}}
	}
	report := previewRouteReport{Outcome: "request_breaking_changes", Requests: previewReportEvidence{Status: "available"}, Routes: []previewReportRoute{
		row("/unknown", "unknown", false, 1000), row("/small-break", "breaking", true, 1), row("/large-break", "breaking", true, 100), row("/widening", "no_supported_breaks", true, 9999),
	}}
	prioritizePreviewRouteReview(&report)
	if len(report.ReviewPriorities) != 3 || report.ReviewPriorities[0].Path != "/large-break" || report.ReviewPriorities[1].Path != "/small-break" || report.ReviewPriorities[2].Priority != "needs_evidence" {
		t.Fatal(report.ReviewPriorities)
	}
	report = previewRouteReport{Outcome: "no_findings", Requests: previewReportEvidence{Status: "available"}, Routes: []previewReportRoute{row("/widening", "no_supported_breaks", true, 1)}}
	report.Routes[0].SourceImpact = &previewRouteSource{Change: "direct"}
	prioritizePreviewRouteReview(&report)
	if report.Outcome != "review_required" || len(report.ReviewPriorities) != 1 {
		t.Fatalf("request widening hid independent source review: %+v", report)
	}
	report.Requirements = &routerequirements.PreviewReport{Report: routerequirements.Report{Routes: []routerequirements.RouteResult{{Method: "POST", Path: "/widening", Status: "violated"}}}}
	prioritizePreviewRouteReview(&report)
	if report.ReviewPriorities[0].Priority != "blocker" {
		t.Fatal(report.ReviewPriorities)
	}
}

func TestPreviewRequestFindingsRenderWithoutSchemaValues(t *testing.T) {
	field := "currency|<script>"
	before := previewReportSpec(t, previewRequestDocument(t, map[string]any{"type": "object", "properties": map[string]any{field: map[string]any{"type": "string", "enum": []any{"private-a", "private-b"}}}}))
	after := previewReportSpec(t, previewRequestDocument(t, map[string]any{"type": "object", "properties": map[string]any{field: map[string]any{"type": "string", "enum": []any{"private-a"}}}, "required": []any{field}}))
	rows, evidence := comparePreviewContractsWithRequests(before, after)
	report := previewRouteReport{Outcome: "request_breaking_changes", Requests: evidence, Routes: rows}
	prioritizePreviewRouteReview(&report)
	var out bytes.Buffer
	renderPreviewRouteReport(&out, report, true)
	if strings.Contains(out.String(), "private-") || strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "; )") || !strings.Contains(out.String(), "Request contract findings") || !strings.Contains(out.String(), "blocker") || !strings.Contains(out.String(), "This request field became required.") {
		t.Fatalf("unsafe or incomplete request output: %s", out.String())
	}
}
