package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const previewReviewContract = `{"openapi":"3.1.0","info":{"title":"demo","version":"1"},"paths":{"/users/{id}":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string"}}}}}}}}}}}`
const previewReviewChangedContract = `{"openapi":"3.1.0","info":{"title":"demo","version":"2"},"paths":{"/users/{id}":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"integer"}}}}}}}}}}}`

const (
	previewReviewAPIBaseline    = "11111111-1111-4111-8111-111111111111"
	previewReviewWorkerBaseline = "22222222-2222-4222-8222-222222222222"
)

func TestPreviewReviewAggregatesAppsAndRanksEqualRiskByObservedReach(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("release review mutated state with %s %s", r.Method, r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			writePreviewReviewResource(w, "pr-42-api", "api")
		case "/v1/preview/pr-42-worker":
			writePreviewReviewResource(w, "pr-42-worker", "worker")
		case "/v1/apps/api/deployments/" + previewReviewAPIBaseline + "/openapi":
			writePreviewReportDoc(t, w, previewReviewAPIBaseline, previewReviewContract)
		case "/v1/apps/pr-42-api/deployments/candidate-api/openapi":
			writePreviewReportDoc(t, w, "candidate-api", previewReviewChangedContract)
		case "/v1/apps/worker/deployments/" + previewReviewWorkerBaseline + "/openapi":
			writePreviewReportDoc(t, w, previewReviewWorkerBaseline, previewReviewContract)
		case "/v1/apps/pr-42-worker/deployments/candidate-worker/openapi":
			writePreviewReportDoc(t, w, "candidate-worker", previewReviewChangedContract)
		case "/v1/apps/api/deployments/" + previewReviewAPIBaseline + "/route-policy":
			writePreviewPolicySnapshotTest(w, previewReviewAPIBaseline, "parent-api", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/deployments/candidate-api/route-policy":
			writePreviewPolicySnapshotTest(w, "candidate-api", "preview-api", []api.EdgeRuleResponse{})
		case "/v1/apps/worker/deployments/" + previewReviewWorkerBaseline + "/route-policy":
			writePreviewPolicySnapshotTest(w, previewReviewWorkerBaseline, "parent-worker", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-worker/deployments/candidate-worker/route-policy":
			writePreviewPolicySnapshotTest(w, "candidate-worker", "preview-worker", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/openapi/preview", "/v1/apps/pr-42-worker/openapi/preview":
			writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{Routes: []api.AppOpenAPIPolicyPreviewRoute{}})
		case "/v1/apps/api/edge-rules", "/v1/apps/pr-42-api/edge-rules", "/v1/apps/worker/edge-rules", "/v1/apps/pr-42-worker/edge-rules":
			writeJSONTest(w, []api.EdgeRuleResponse{})
		case "/v1/apps/api/analytics":
			writeJSONTest(w, previewReportAnalytics(previewReviewAPIBaseline, 30, 70))
		case "/v1/apps/pr-42-api/analytics":
			writeJSONTest(w, previewReportAnalytics("candidate-api", 30, 70))
		case "/v1/apps/worker/analytics":
			writeJSONTest(w, previewReportAnalytics(previewReviewWorkerBaseline, 25, 45))
		case "/v1/apps/pr-42-worker/analytics":
			writeJSONTest(w, previewReportAnalytics("candidate-worker", 25, 45))
		case "/v1/apps/api/analytics/route-customers":
			writePreviewReviewCustomerUsage(w, r, "api", previewReviewAPIBaseline, 12, "private-api-consumer")
		case "/v1/apps/worker/analytics/route-customers":
			writePreviewReviewCustomerUsage(w, r, "worker", previewReviewWorkerBaseline, 2, "private-worker-consumer")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	var out bytes.Buffer
	oldOut := osStdout
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true

	if code := cmdPreview([]string{"review", "pr-42-api", "pr-42-worker"}); code != 0 {
		t.Fatalf("exit = %d; %s", code, out.String())
	}
	var report previewReleaseRouteReview
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != 1 || report.Outcome != "review_required" || report.Summary.PreviewCount != 2 || report.Summary.AvailablePreviews != 2 || report.Summary.CapturedRoutes != 2 || report.Summary.ReviewItems != 2 {
		t.Fatalf("release review summary = %+v", report)
	}
	if len(report.Previews) != 2 || report.Previews[0].Slug != "pr-42-api" || report.Previews[1].Slug != "pr-42-worker" {
		t.Fatalf("previews lost caller order or app identity: %+v", report.Previews)
	}
	for _, preview := range report.Previews {
		if preview.Report == nil || len(preview.Report.Routes) != 1 || preview.Report.Routes[0].Path != "/users/{id}" {
			t.Fatalf("route identity was not kept within its app: %+v", preview)
		}
	}
	if len(report.ReviewPriorities) != 2 || report.ReviewPriorities[0].Preview != "pr-42-api" || report.ReviewPriorities[0].CustomerImpact == nil || report.ReviewPriorities[0].CustomerImpact.Usage.PlatformTenantCount != 12 {
		t.Fatalf("release-wide customer exposure ordering = %+v", report.ReviewPriorities)
	}
	if strings.Contains(out.String(), "private-api-consumer") || strings.Contains(out.String(), "private-worker-consumer") {
		t.Fatal("customer identities leaked without --customer-details")
	}
}

func writePreviewReviewResource(w http.ResponseWriter, slug, parent string) {
	baseline := previewReviewAPIBaseline
	if parent == "worker" {
		baseline = previewReviewWorkerBaseline
	}
	writeJSONTest(w, api.PreviewResourceResponse{
		App:                  api.AppResponse{ID: "preview-" + parent, Slug: slug, PreviewOfSlug: parent},
		Parent:               &api.AppResponse{ID: "parent-" + parent, Slug: parent},
		LatestDeployment:     &api.DeploymentResponse{ID: "candidate-" + parent, AppID: "preview-" + parent, Status: "live"},
		ProductionDeployment: &api.DeploymentResponse{ID: baseline, AppID: "parent-" + parent, Status: "live"},
	})
}

func writePreviewReviewCustomerUsage(w http.ResponseWriter, r *http.Request, slug, deployment string, tenants int64, customerID string) {
	until := r.URL.Query().Get("until")
	parsedUntil, _ := time.Parse(time.RFC3339Nano, until)
	writeJSONTest(w, api.RouteCustomerUsageResponse{
		Slug: slug, DeploymentID: deployment, From: parsedUntil.Add(-time.Hour).Format(time.RFC3339Nano),
		Until: until, Coverage: "observed_only", Routes: []api.RouteCustomerUsage{{
			Route: "GET /users/{id}", Method: "GET", Requests: tenants * 10, IdentifiedRequests: tenants * 10,
			ConsumerCount: tenants, PlatformTenantCount: tenants,
			Customers: []api.RouteCustomerObservation{{ConsumerID: customerID, Requests: tenants * 10}},
		}},
	})
}

func TestPreviewReviewAssignmentValidationAndGates(t *testing.T) {
	slugs := map[string]struct{}{"pr-42-api": {}, "pr-42-worker": {}}
	assignments, err := parsePreviewReviewAssignments([]string{"pr-42-api=api.json", "pr-42-worker=worker.json"}, slugs, "source impact")
	if err != nil || assignments["pr-42-worker"] != "worker.json" {
		t.Fatalf("assignments=%v err=%v", assignments, err)
	}
	for _, values := range [][]string{
		{"pr-42-api=one.json", "pr-42-api=two.json"},
		{"pr-99-other=one.json"},
		{"not-an-assignment"},
	} {
		if _, err := parsePreviewReviewAssignments(values, slugs, "source impact"); err == nil {
			t.Errorf("accepted invalid assignments %v", values)
		}
	}

	breaking := previewRouteReport{Outcome: "breaking_changes", Routes: []previewReportRoute{{Breaks: []previewReportBreak{{Kind: "response_schema", Status: "breaking"}}}}}
	if !previewReleaseReviewFails(previewReleaseRouteReview{Previews: []previewReleaseRouteReviewPreview{{Report: &breaking}}}, previewReleaseReviewGates{breaking: true}) {
		t.Fatal("release gate ignored a child response break")
	}
	if !previewReleaseReviewFails(previewReleaseRouteReview{Previews: []previewReleaseRouteReviewPreview{{Slug: "pr-42-worker"}}}, previewReleaseReviewGates{incomplete: true}) {
		t.Fatal("release incomplete gate ignored an unavailable preview")
	}
	if !previewReleaseReviewFails(previewReleaseRouteReview{Previews: []previewReleaseRouteReviewPreview{{Slug: "pr-42-worker"}}}, previewReleaseReviewGates{}) {
		t.Fatal("unavailable preview did not fail the aggregate command")
	}
	if previewReleaseReviewFails(previewReleaseRouteReview{Previews: []previewReleaseRouteReviewPreview{{Report: &breaking}}}, previewReleaseReviewGates{}) {
		t.Fatal("ungated review unexpectedly failed")
	}
}

func TestPreviewReviewBoundsCombinedEvidenceFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large-evidence.json")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(previewReviewEvidenceMaxBytes + 1); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validatePreviewReviewEvidenceSize(map[string]string{"pr-42-api": path}); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("oversized release evidence accepted: %v", err)
	}
}

func TestPreviewReleaseReviewMarkdownEscapesPriorityRoute(t *testing.T) {
	report := previewReleaseRouteReview{
		Outcome: "review_required",
		Summary: previewReleaseRouteReviewSummary{PreviewCount: 1, UnavailablePreviews: 1, ReviewItems: 1},
		ReviewPriorities: []previewReleaseReviewPriority{{
			Preview: "pr-42-api", Method: "GET", Path: "/users|<script>", Scope: "captured_route",
			Priority: "blocker", Reasons: []string{"contract_compatibility_break"},
		}},
		Previews: []previewReleaseRouteReviewPreview{{Slug: "pr-42-api", Status: "unavailable", Reason: "preview_or_evidence_unavailable"}},
	}
	var out bytes.Buffer
	renderPreviewReleaseRouteReview(&out, report, true)
	if strings.Contains(out.String(), "<script>") || !strings.Contains(out.String(), `GET /users\|&lt;script&gt;`) {
		t.Fatalf("unsafe or missing escaped route in markdown: %s", out.String())
	}
}
