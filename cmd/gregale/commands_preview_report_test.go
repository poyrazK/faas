package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/openapidiff"
)

func previewReportSpec(t *testing.T, body string) *openapidiff.Spec {
	t.Helper()
	spec, err := openapidiff.LoadBytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

const previewReportBefore = `{"openapi":"3.1.0","info":{"title":"demo","version":"1"},"paths":{"/users/{id}":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"string","example":"secret-example"}}}}}}}}},"/old":{"delete":{"responses":{"204":{"description":"ok"}}}}}}`
const previewReportAfter = `{"openapi":"3.1.0","info":{"title":"demo","version":"2"},"paths":{"/users/{id}":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"name":{"type":"integer","example":"secret-example"}}}}}}}}},"/new":{"post":{"responses":{"201":{"description":"ok"}}}}}}`

func TestPreviewReportComparesCapturedRevisionsAndRedactsActions(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodGet {
			t.Errorf("report mutated state: %s", r.Method)
		}
		switch r.URL.Path {
		case "/v1/preview/pr-42-api":
			writeJSONTest(w, api.PreviewResourceResponse{
				App:                  api.AppResponse{ID: "preview-id", Slug: "pr-42-api", PreviewOfSlug: "api"},
				Parent:               &api.AppResponse{ID: "parent-id", Slug: "api"},
				LatestDeployment:     &api.DeploymentResponse{ID: "candidate", AppID: "preview-id", Status: "live"},
				ProductionDeployment: &api.DeploymentResponse{ID: "baseline", AppID: "parent-id", Status: "live"},
			})
		case "/v1/apps/api/deployments/baseline/openapi":
			writePreviewReportDoc(t, w, "baseline", previewReportBefore)
		case "/v1/apps/pr-42-api/deployments/candidate/openapi":
			writePreviewReportDoc(t, w, "candidate", previewReportAfter)
		case "/v1/apps/api/deployments/baseline/route-policy":
			writePreviewPolicySnapshotTest(w, "baseline", "parent-id", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/deployments/candidate/route-policy":
			writePreviewPolicySnapshotTest(w, "candidate", "preview-id", []api.EdgeRuleResponse{})
		case "/v1/apps/pr-42-api/openapi/preview":
			writeJSONTest(w, api.AppOpenAPIPolicyPreviewResponse{Routes: []api.AppOpenAPIPolicyPreviewRoute{{
				Path: "/users/{id}", Method: "get", Rules: []api.AppOpenAPIPolicyPreviewRule{{Kind: "jwt", Enabled: true, Action: json.RawMessage(`{"token":"secret-action"}`)}},
			}}})
		case "/v1/apps/api/edge-rules", "/v1/apps/pr-42-api/edge-rules":
			writeJSONTest(w, []api.EdgeRuleResponse{})
		case "/v1/apps/api/analytics":
			writeJSONTest(w, previewReportAnalytics("baseline", 100, 40))
		case "/v1/apps/pr-42-api/analytics":
			writeJSONTest(w, previewReportAnalytics("candidate", 120, 20))
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
	if code := cmdPreview([]string{"report", "pr-42-api", "--fail-on-breaking"}); code != 1 {
		t.Fatalf("exit = %d, want 1; %s", code, out.String())
	}
	var report previewRouteReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != 7 || report.SourceImpact != nil || report.Outcome != "breaking_changes" || report.Contract.Status != "available" || report.BaselineDeployment != "baseline" {
		t.Fatalf("report = %+v", report)
	}
	if len(paths) != 9 {
		t.Fatalf("unexpected reads: %v", paths)
	}
	row := findPreviewReportRoute(t, report, "GET /users/{id}")
	if len(row.Breaks) != 1 || len(row.PolicyKinds) != 1 || row.PolicyKinds[0] != "jwt" || row.P95ChangeMS == nil || *row.P95ChangeMS != 20 {
		t.Fatalf("route = %+v", row)
	}
	if strings.Contains(out.String(), "secret-example") || strings.Contains(out.String(), "secret-action") {
		t.Fatal("shareable report leaked schema examples or policy actions")
	}
}

func writePreviewPolicySnapshotTest(w http.ResponseWriter, deploymentID, appID string, rules []api.EdgeRuleResponse) {
	writeJSONTest(w, api.DeploymentRoutePolicySnapshotResponse{
		DeploymentID: deploymentID, AppID: appID, Scope: "prod",
		SHA256: strings.Repeat("0", 64), SchemaVersion: 1, CapturedAt: time.Now().UTC(),
		Rules: rules,
	})
}

func writePreviewReportDoc(t *testing.T, w http.ResponseWriter, id, body string) {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: id, Doc: doc})
}

func previewReportAnalytics(id string, p95 int, requests int64) api.RequestAnalyticsResponse {
	return api.RequestAnalyticsResponse{From: "2026-10-01T00:00:00Z", Until: "2026-10-01T01:00:00Z", Routes: []api.RequestAnalyticsRoute{{
		Route: "GET /users/{id}", Method: "GET", Requests: requests, P95MS: p95,
		DeploymentObservations: []api.RequestAnalyticsRouteDeploymentObservation{{DeploymentID: id, Requests: requests}},
	}}}
}

func findPreviewReportRoute(t *testing.T, report previewRouteReport, key string) previewReportRoute {
	t.Helper()
	for _, row := range report.Routes {
		if previewReportRouteKey(row.Method, row.Path) == key {
			return row
		}
	}
	t.Fatalf("missing route %q in %+v", key, report.Routes)
	return previewReportRoute{}
}

func TestPreviewReportRouteChangesAndRequestReview(t *testing.T) {
	before, after := previewReportSpec(t, previewReportBefore), previewReportSpec(t, previewReportAfter)
	report := previewRouteReport{Routes: comparePreviewReportContracts(before, after)}
	if got := findPreviewReportRoute(t, report, "DELETE /old"); got.Change != "removed" || len(got.Breaks) == 0 {
		t.Fatalf("removed = %+v", got)
	}
	if got := findPreviewReportRoute(t, report, "POST /new"); got.Change != "added" || len(got.Breaks) != 0 {
		t.Fatalf("added = %+v", got)
	}
	unchanged := comparePreviewReportContracts(before, before)
	for _, route := range unchanged {
		if route.Change != "unchanged" || route.RequestContractChanged {
			t.Fatalf("unchanged contract reported changes: %+v", route)
		}
	}
	after.Paths["/users/{id}"].Methods["get"].Raw["requestBody"] = map[string]any{"required": true}
	report.Routes = comparePreviewReportContracts(before, after)
	if !findPreviewReportRoute(t, report, "GET /users/{id}").RequestContractChanged {
		t.Fatal("request changes must be marked for review")
	}
}

func TestPreviewReportRejectsMixedOrUnattributedTraffic(t *testing.T) {
	for _, mutate := range []func(*api.RequestAnalyticsRoute){
		func(row *api.RequestAnalyticsRoute) { row.DeploymentObservations = nil },
		func(row *api.RequestAnalyticsRoute) { row.DeploymentObservations[0].DeploymentID = "older" },
		func(row *api.RequestAnalyticsRoute) { row.DeploymentObservations[0].Requests-- },
		func(row *api.RequestAnalyticsRoute) { row.OtherDeploymentRequests = 1 },
		func(row *api.RequestAnalyticsRoute) {
			row.DeploymentObservations = append(row.DeploymentObservations, api.RequestAnalyticsRouteDeploymentObservation{DeploymentID: "older"})
		},
	} {
		data := previewReportAnalytics("candidate", 120, 20)
		mutate(&data.Routes[0])
		if got := previewReportTrafficRows(data, "candidate"); len(got) != 0 {
			t.Fatalf("mixed traffic became candidate evidence: %+v", got)
		}
	}
}

func TestPreviewReportTestsRequireMatchingDeploymentAndApp(t *testing.T) {
	report := previewRouteReport{Preview: "pr-42-api", CandidateDeployment: "candidate", Routes: []previewReportRoute{*newPreviewReportRoute("GET", "/users/{id}")}}
	start := time.Now().UTC()
	receipt := testRunReceipt{Engine: "real-vm", AppSlug: report.Preview, DeploymentID: "different", Profile: "restored", Status: "passed", StartedAt: start, FinishedAt: start.Add(time.Second),
		Requests: []testHTTPRequestEvidence{{Method: "GET", Path: "/users/123?token=secret", Passed: true}}}
	attachPreviewReportTests(&report, []testRunReceipt{receipt}, "digest")
	if report.UnboundTestRuns != 1 || len(report.Routes[0].TestProfiles) != 0 {
		t.Fatalf("unrelated tests became coverage: %+v", report)
	}
	receipt.DeploymentID = report.CandidateDeployment
	attachPreviewReportTests(&report, []testRunReceipt{receipt}, "digest")
	if report.Tests.Status != "available" || len(report.Routes[0].TestProfiles) != 1 || report.Routes[0].TestProfiles[0].Passed != 1 {
		t.Fatalf("matching test not attached: %+v", report)
	}
	receipt.CleanupError = "secret failure details"
	attachPreviewReportTests(&report, []testRunReceipt{receipt}, "digest")
	if report.Routes[0].TestProfiles[0].Failed != 1 {
		t.Fatal("failed cleanup must not produce passing coverage")
	}
	body, _ := json.Marshal(report)
	if strings.Contains(string(body), "secret") {
		t.Fatal("report included request query or test failure details")
	}
}

func TestPreviewReportPathMatchesExactTemplate(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{{"/users/123", true}, {"/users/123?q=secret", true}, {"/users/", false}, {"/users/123/extra", false}, {"https://other.example/users/123", false}} {
		if got := previewReportPathMatches("/users/{id}", tc.path); got != tc.want {
			t.Errorf("%q: got %t, want %t", tc.path, got, tc.want)
		}
	}
}

func TestPreviewReportMarkdownEscapesRouteAndUnknownEvidence(t *testing.T) {
	report := previewRouteReport{Preview: "pr-42-api", Outcome: "incomplete", Routes: []previewReportRoute{*newPreviewReportRoute("GET", "/a|<script>\x1b")}}
	var out bytes.Buffer
	renderPreviewRouteReport(&out, report, true)
	if strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), "not established") {
		t.Fatalf("unsafe or misleading markdown: %s", out.String())
	}
}

func TestPreviewReportUnavailableContractDoesNotMeanNoChanges(t *testing.T) {
	report := previewRouteReport{Contract: previewReportEvidence{Status: "unavailable"}, Routes: []previewReportRoute{}}
	finishPreviewRouteReport(&report)
	if report.Outcome != "incomplete" {
		t.Fatalf("missing evidence got %q", report.Outcome)
	}
}

func TestPreviewReportSourceMatchedTestsStaySupplemental(t *testing.T) {
	start := time.Now().UTC()
	digest := strings.Repeat("a", 64)
	report := previewRouteReport{Preview: "pr-42-api", CandidateDeployment: "candidate", CandidateSourceSHA256: digest,
		Routes: []previewReportRoute{*newPreviewReportRoute("GET", "/users/{id}")}}
	receipt := testRunReceipt{Engine: "real-vm", AppSlug: "isolated-test", DeploymentID: "test-deploy", SourceSHA256: digest,
		Profile: "cold", Status: "passed", StartedAt: start, FinishedAt: start.Add(time.Second),
		Requests: []testHTTPRequestEvidence{{Method: "GET", Path: "/users/123", Passed: true}}}
	attachPreviewReportTests(&report, []testRunReceipt{receipt}, "report-digest")
	if report.Tests.Status != "supplemental" || len(report.Routes[0].TestProfiles) != 0 || len(report.Routes[0].SourceTestProfiles) != 1 {
		t.Fatalf("source match became exact candidate coverage: %+v", report)
	}
	for _, bad := range []string{"", "short", strings.Repeat("z", 64), strings.Repeat("b", 64)} {
		if previewReportSourceMatches(digest, bad) {
			t.Errorf("invalid source match %q", bad)
		}
	}
}

func TestPreviewReportExplicitBaselineMustBelongToParent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, api.DeploymentResponse{ID: "other-deploy", AppID: "other-app"})
	}))
	defer srv.Close()
	if _, err := previewReportBaseline(t.Context(), api.NewClient(srv.URL, "token"), "other-deploy", "parent-app"); err == nil {
		t.Fatal("accepted another app's deployment as the parent baseline")
	}
}

func TestPreviewReportTruncatedDocumentIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSONTest(w, api.OpenAPIDocResponse{DeploymentID: "candidate", Truncated: true, Doc: map[string]any{"openapi": "3.1.0"}})
	}))
	defer srv.Close()
	spec, hash, state := previewReportDocument(t.Context(), api.NewClient(srv.URL, "token"), "app", "candidate")
	if spec != nil || hash != "" || state != "document_incomplete" {
		t.Fatalf("truncated document became a contract: %v %q %q", spec, hash, state)
	}
}

func TestPreviewReportCLIIncompleteGateEmitsOutput(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/preview/pr-42-api" {
			writeJSONTest(w, api.PreviewResourceResponse{App: api.AppResponse{Slug: "pr-42-api", PreviewOfSlug: "api"}, Parent: &api.AppResponse{Slug: "api"}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	oldOut := osStdout
	var out bytes.Buffer
	osStdout = &out
	t.Cleanup(func() { osStdout = oldOut })
	jsonOutput = true
	if code := cmdPreviewReport([]string{"pr-42-api", "--fail-on-incomplete"}); code != 1 {
		t.Fatalf("missing evidence exit = %d; %s", code, out.String())
	}
	var report previewRouteReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Outcome != "incomplete" {
		t.Fatalf("gate did not retain incomplete report: %v %s", err, out.String())
	}
}

func TestPreviewReportCurrentRoutesCannotEstablishContractChanges(t *testing.T) {
	report := previewRouteReport{Routes: []previewReportRoute{}, Contract: previewReportEvidence{Status: "unavailable"}}
	policy := api.AppOpenAPIPolicyPreviewResponse{Routes: []api.AppOpenAPIPolicyPreviewRoute{{Path: "/observed", Method: "post", Observed: true,
		Rules: []api.AppOpenAPIPolicyPreviewRule{{Kind: "throttle", Enabled: true}}}}}
	attachPreviewReportPolicy(&report, policy, nil)
	finishPreviewRouteReport(&report)
	row := findPreviewReportRoute(t, report, "POST /observed")
	if row.Change != "unknown" || row.RouteSource != "current_policy_or_observation" || len(row.PolicyKinds) != 1 || report.Outcome != "incomplete" {
		t.Fatalf("current route became captured revision evidence: %+v", report)
	}
	if len(row.NextActions) == 0 {
		t.Fatal("unknown route needs a capture next action")
	}
}

func TestPreviewReportInvalidOptionsFailBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{
		{}, {"INVALID"}, {"pr-42-api", "--since", "0s"}, {"pr-42-api", "--format", "html"},
		{"pr-42-api", "--baseline-deployment", "../other"},
	} {
		if code := cmdPreviewReport(args); code != 1 {
			t.Errorf("%v: exit = %d, want 1", args, code)
		}
	}
}
