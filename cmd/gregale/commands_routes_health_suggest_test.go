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

func routeHealthSuggestionFixture() api.RouteCustomerUsageResponse {
	usage := customerUsageFixture()
	usage.AsOf = "2026-10-05T00:00:00Z"
	usage.From = "2026-09-28T00:00:00Z"
	usage.Until = usage.AsOf
	usage.RoutesTruncated = true
	usage.Routes = []api.RouteCustomerUsage{
		{Route: "POST /checkout", Method: "POST", Requests: 39, IdentifiedRequests: 34, AnonymousRequests: 3, UnresolvedIdentityRequests: 2,
			ConsumerCount: 3, PlatformTenantCount: 2, LastObservedAt: "2026-10-04T23:58:00Z", Customers: []api.RouteCustomerObservation{{ConsumerID: customerID, PlatformTenantID: customerTenant, Requests: 20, LastObservedAt: "2026-10-04T23:58:00Z"}}},
		{Route: "GET /users/{id}", Method: "GET", Requests: 60, IdentifiedRequests: 55, AnonymousRequests: 3, UnresolvedIdentityRequests: 2,
			ConsumerCount: 8, PlatformTenantCount: 4, LastObservedAt: "2026-10-04T23:59:00Z", Customers: []api.RouteCustomerObservation{{ConsumerID: customerID, PlatformTenantID: customerTenant, Requests: 30, LastObservedAt: "2026-10-04T23:59:00Z"}}},
		{Route: "GET /health", Method: "GET", Requests: 50, IdentifiedRequests: 50,
			ConsumerCount: 4, PlatformTenantCount: 4, LastObservedAt: "2026-10-04T23:57:00Z", Customers: []api.RouteCustomerObservation{}},
	}
	return usage
}

func TestBuildRouteHealthSuggestionReportRanksExposureAndRedactsCustomerDetails(t *testing.T) {
	usage := routeHealthSuggestionFixture()
	report, err := buildRouteHealthSuggestionReport(usage, "api", customerBaseline, "tenant", 3)
	if err != nil {
		t.Fatal(err)
	}
	if report.Suggestions[0].Selector.Path != "/users/{id}" || report.Suggestions[1].Selector.Path != "/health" || report.Suggestions[2].Selector.Path != "/checkout" {
		t.Fatalf("tenant ranking = %+v", report.Suggestions)
	}
	if report.Suggestions[0].SampleAssessment != "window_distribution_unknown" || report.Suggestions[2].SampleAssessment != "below_historical_two_window_request_floor" {
		t.Fatalf("sample assessments = %+v", report.Suggestions)
	}
	if !report.Observation.RoutesTruncated || report.Observation.RoutesScanned != 3 || report.Observation.RouteLimit != 200 {
		t.Fatalf("observation bounds = %+v", report.Observation)
	}
	consumerReport, err := buildRouteHealthSuggestionReport(usage, "api", customerBaseline, "consumer", 2)
	if err != nil {
		t.Fatal(err)
	}
	if consumerReport.Suggestions[0].Selector.Path != "/users/{id}" || consumerReport.Suggestions[1].Selector.Path != "/health" {
		t.Fatalf("consumer ranking = %+v", consumerReport.Suggestions)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{customerID, customerTenant} {
		if strings.Contains(string(body), id) {
			t.Fatalf("customer identity leaked in suggestion report: %s", body)
		}
	}
}

func TestBuildRouteHealthSuggestionReportRejectsUnboundOrAmbiguousUsage(t *testing.T) {
	for name, mutate := range map[string]func(*api.RouteCustomerUsageResponse){
		"wrong app":        func(r *api.RouteCustomerUsageResponse) { r.Slug = "other" },
		"wrong deployment": func(r *api.RouteCustomerUsageResponse) { r.DeploymentID = customerID },
		"wrong coverage":   func(r *api.RouteCustomerUsageResponse) { r.Coverage = "complete" },
		"future window":    func(r *api.RouteCustomerUsageResponse) { r.Until = "2026-10-06T00:00:00Z" },
		"duplicate route":  func(r *api.RouteCustomerUsageResponse) { r.Routes = append(r.Routes, r.Routes[0]) },
		"invalid path":     func(r *api.RouteCustomerUsageResponse) { r.Routes[0].Route = "POST /checkout?debug=true" },
	} {
		t.Run(name, func(t *testing.T) {
			usage := routeHealthSuggestionFixture()
			mutate(&usage)
			if _, err := buildRouteHealthSuggestionReport(usage, "api", customerBaseline, "tenant", 10); err == nil {
				t.Fatal("accepted invalid route usage evidence")
			}
		})
	}
}

func TestRoutesHealthSuggestReadsAndWritesReviewableSelectors(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	usage := routeHealthSuggestionFixture()
	var reads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/analytics/route-customers" {
			t.Errorf("unexpected route usage request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer test-token" || r.URL.Query().Get("deployment_id") != customerBaseline || r.URL.Query().Get("since") != "7d" {
			t.Errorf("wrong authorization or usage scope: %s %s", r.Header.Get("Authorization"), r.URL.RawQuery)
		}
		writeJSONTest(w, usage)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)

	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	destination := filepath.Join(t.TempDir(), "routes.json")
	code := cmdRoutesHealth([]string{"suggest", "api", "--deployment", customerBaseline, "--since", "7d", "--customer-group-by", "tenant", "--limit", "2", "--out", destination})
	if code != 0 {
		t.Fatalf("suggest returned %d: %s", code, out.String())
	}
	if reads != 1 {
		t.Fatalf("route usage reads = %d, want one GET", reads)
	}
	var report routeHealthSuggestionReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, out.String())
	}
	if len(report.Selectors) != 2 || report.Selectors[0].Path != "/users/{id}" || !report.Observation.RoutesTruncated {
		t.Fatalf("unexpected report selectors or bounds: %+v", report)
	}
	for _, id := range []string{customerID, customerTenant} {
		if strings.Contains(out.String(), id) {
			t.Fatalf("customer identity leaked in CLI output: %s", out.String())
		}
	}
	fileInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("selector file permissions = %o, want 600", fileInfo.Mode().Perm())
	}
	var selectors []api.RouteHealthRoute
	fileBody, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fileBody, &selectors); err != nil || len(selectors) != 2 || selectors[0].Path != "/users/{id}" {
		t.Fatalf("saved selectors = %s, err %v", fileBody, err)
	}
	if strings.Contains(string(fileBody), customerID) || strings.Contains(string(fileBody), customerTenant) {
		t.Fatal("saved selector file includes customer identities")
	}
}

func releaseSuggestionPreviewFixture() previewRouteReport {
	generated := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	usage := routeHealthSuggestionFixture()
	row := func(method, path, contractChange, sourceChange, precision, match string, customerStatus string, usage *api.RouteCustomerUsage) previewReportRoute {
		return previewReportRoute{Method: method, Path: path, Change: contractChange, RouteSource: "captured_deployment_contract",
			SourceImpact:   &previewRouteSource{Change: sourceChange, Precision: precision, Match: match},
			CustomerImpact: &previewRouteCustomerImpact{Status: customerStatus, Usage: usage}}
	}
	return previewRouteReport{
		Version: 7, Preview: "pr-42-api", Parent: "api", BaselineDeployment: customerBaseline, GeneratedAt: generated,
		SourceImpact: &previewSourceImpact{Status: "aligned", AnalysisStatus: "incomplete", MappingStatus: "partial", IssueCount: 1,
			Base: previewSourceBinding{Status: "declared_match"}, Candidate: previewSourceBinding{Status: "declared_match"},
			Unmatched: []previewUnmatchedSource{{Method: "PATCH", Path: "/unmapped", Reason: "captured_route_missing"}}},
		Routes: []previewReportRoute{
			row("GET", "/users/{id}", "changed", "source_changed", "function", "parameter_names", "observed", &usage.Routes[1]),
			row("POST", "/checkout", "unchanged", "potentially_affected", "mixed", "exact", "observed", &usage.Routes[0]),
			row("GET", "/health", "unchanged", "no_linked_changes", "function", "exact", "observed", &usage.Routes[2]),
			row("DELETE", "/old", "removed", "removed", "function", "exact", "observed", &api.RouteCustomerUsage{Route: "DELETE /old", Method: "DELETE", Requests: 50, IdentifiedRequests: 50, PlatformTenantCount: 9, LastObservedAt: "2026-10-04T23:59:00Z"}),
			row("GET", "/new", "added", "added", "function", "exact", "no_observations", nil),
			row("GET", "/sleepy", "unchanged", "unknown", "module_fallback", "exact", "no_observations", nil),
		},
	}
}

func TestBuildReleaseRouteHealthSuggestionReportScopesToMappedSourceChanges(t *testing.T) {
	usage := routeHealthSuggestionFixture()
	preview := releaseSuggestionPreviewFixture()
	report, err := buildReleaseRouteHealthSuggestionReport(usage, "api", customerBaseline, "tenant", 10, preview)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Suggestions) != 2 || report.Suggestions[0].Selector.Path != "/users/{id}" || report.Suggestions[1].Selector.Path != "/checkout" {
		t.Fatalf("release suggestions = %+v", report.Suggestions)
	}
	if report.Suggestions[0].SourceChange != "source_changed" || report.Suggestions[0].SourceMatch != "parameter_names" || report.Suggestions[1].SourceChange != "potentially_affected" {
		t.Fatalf("source evidence missing: %+v", report.Suggestions)
	}
	scope := report.ReleaseScope
	if scope == nil || scope.MappedSourceRoutes != 6 || scope.AffectedSourceRoutes != 3 || scope.ObservedAffectedRoutes != 2 ||
		scope.UnobservedAffectedRoutes != 1 || scope.StructuralRoutesSkipped != 2 || scope.UnmatchedSourceRoutes != 1 {
		t.Fatalf("release scope = %+v", scope)
	}
	if !report.Observation.RoutesTruncated || len(report.Caveats) < 3 {
		t.Fatalf("release caveats or inventory bounds absent: %+v", report)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{customerID, customerTenant} {
		if strings.Contains(string(body), id) {
			t.Fatalf("customer identity leaked in release suggestions: %s", body)
		}
	}
}

func TestRoutesHealthSuggestUsesBoundPreviewReportAndWritesOnlyAffectedSelectors(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	preview := releaseSuggestionPreviewFixture()
	previewBody, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	previewPath := filepath.Join(t.TempDir(), "preview-report.json")
	if err := os.WriteFile(previewPath, previewBody, 0o600); err != nil {
		t.Fatal(err)
	}
	usage := routeHealthSuggestionFixture()
	var reads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/analytics/route-customers" ||
			r.URL.Query().Get("deployment_id") != customerBaseline || r.URL.Query().Get("since") != "7d" {
			t.Errorf("unexpected route usage request: %s %s", r.Method, r.URL.String())
		}
		writeJSONTest(w, usage)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)

	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	destination := filepath.Join(t.TempDir(), "affected-routes.json")
	code := cmdRoutesHealth([]string{"suggest", "api", "--deployment", customerBaseline, "--since", "7d", "--preview-report", previewPath, "--out", destination})
	if code != 0 {
		t.Fatalf("release-scoped suggest returned %d: %s", code, out.String())
	}
	if reads != 1 {
		t.Fatalf("route usage reads = %d, want one read", reads)
	}
	var report routeHealthSuggestionReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v (%s)", err, out.String())
	}
	if report.ReleaseScope == nil || report.ReleaseScope.Preview != "pr-42-api" || len(report.Selectors) != 2 {
		t.Fatalf("release scope or selectors = %+v", report)
	}
	for _, id := range []string{customerID, customerTenant} {
		if strings.Contains(out.String(), id) {
			t.Fatalf("customer identity leaked in CLI output: %s", out.String())
		}
	}
	fileBody, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var selectors []api.RouteHealthRoute
	if err := json.Unmarshal(fileBody, &selectors); err != nil || len(selectors) != 2 || selectors[0].Path != "/users/{id}" || selectors[1].Path != "/checkout" {
		t.Fatalf("saved affected selectors = %s, err %v", fileBody, err)
	}
}

func TestRoutesHealthSuggestRejectsInvalidFlagsBeforeReading(t *testing.T) {
	setPreviewTestAuth(t)
	var stdout, stderr bytes.Buffer
	oldOut, oldErr := osStdout, osStderr
	osStdout, osStderr = &stdout, &stderr
	t.Cleanup(func() { osStdout, osStderr = oldOut, oldErr })
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads++ }))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	for _, flags := range [][]string{
		{"--deployment", "not-a-uuid"},
		{"--deployment", customerBaseline, "--since", "0d"},
		{"--deployment", customerBaseline, "--customer-group-by", "account"},
		{"--deployment", customerBaseline, "--limit", "21"},
	} {
		args := append([]string{"suggest", "api"}, flags...)
		if code := cmdRoutesHealth(args); code != 1 {
			t.Fatalf("%v returned %d, want validation failure", flags, code)
		}
	}
	if reads != 0 {
		t.Fatalf("invalid flags triggered %d API reads", reads)
	}
}
