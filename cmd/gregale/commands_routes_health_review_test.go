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
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func releaseRouteHealthReviewFixture() (api.RouteHealthReport, previewRouteReport) {
	preview := releaseSuggestionPreviewFixture()
	preview.CandidateDeployment = healthCandidateID
	for i := range preview.Routes {
		if impact := preview.Routes[i].CustomerImpact; impact != nil && impact.Usage != nil {
			for j := range impact.Usage.Customers {
				impact.Usage.Customers[j].ConsumerID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
				impact.Usage.Customers[j].PlatformTenantID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
			}
		}
	}

	health := cliHealthReport("healthy")
	health.StableDeploymentID = customerBaseline
	users := api.RouteHealthFinding{Method: "GET", Path: "/users/{id}", Windows: routehealth.Windows(health.CheckedAt)}
	for i := range users.Windows {
		users.Windows[i].Candidate.Requests = 100
		users.Windows[i].Candidate.ServerErrors = 10
		users.Windows[i].Stable.Requests = 100
	}
	health.Routes = append(health.Routes, users)
	anchor := health.CheckedAt.Add(-time.Hour)
	routehealth.Evaluate(&health, &anchor, "")
	return health, preview
}

func TestBuildReleaseRouteHealthReviewJoinsImpactGateAndEvidence(t *testing.T) {
	health, preview := releaseRouteHealthReviewFixture()
	report, err := buildReleaseRouteHealthReviewReport(health, "api", healthCandidateID, preview)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "regressed" || !report.ReleaseScope.Bound || report.ReleaseScope.AffectedExistingRoutes != 3 ||
		report.ReleaseScope.SelectedAffectedRoutes != 2 || report.ReleaseScope.UnselectedAffectedRoutes != 1 ||
		report.ReleaseScope.HealthyAffectedRoutes != 1 || report.ReleaseScope.RegressedAffectedRoutes != 1 ||
		report.ReleaseScope.InsufficientAffectedRoutes != 1 || report.ReleaseScope.UnobservedAffectedRoutes != 1 ||
		report.ReleaseScope.StructuralRoutesSkipped != 2 || len(report.ReleaseScope.UnmatchedSourceRoutes) != 1 {
		t.Fatalf("review scope/status = %+v / %s", report.ReleaseScope, report.Status)
	}
	routes := map[string]routeHealthReviewRoute{}
	for _, route := range report.Routes {
		routes[route.Path] = route
	}
	if len(report.Routes) != 3 || routes["/users/{id}"].Status != "regressed" || routes["/users/{id}"].Evidence == nil ||
		routes["/checkout"].Status != "healthy" || routes["/checkout"].Evidence == nil ||
		routes["/sleepy"].Coverage != "unselected" || routes["/sleepy"].Status != "insufficient_evidence" {
		t.Fatalf("review routes = %+v", report.Routes)
	}
	body, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"} {
		if strings.Contains(string(body), id) {
			t.Fatalf("customer identity leaked in release review: %s", body)
		}
	}
}

func TestBuildReleaseRouteHealthReviewWithholdsVerdictsOnDeploymentMismatch(t *testing.T) {
	for name, mismatch := range map[string]func(*api.RouteHealthReport, *previewRouteReport){
		"stable deployment": func(health *api.RouteHealthReport, _ *previewRouteReport) {
			health.StableDeploymentID = healthCandidateID
		},
		"candidate deployment": func(_ *api.RouteHealthReport, preview *previewRouteReport) {
			preview.CandidateDeployment = customerTenant
		},
	} {
		t.Run(name, func(t *testing.T) {
			health, preview := releaseRouteHealthReviewFixture()
			mismatch(&health, &preview)
			report, err := buildReleaseRouteHealthReviewReport(health, "api", healthCandidateID, preview)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != "insufficient_evidence" || report.ReleaseScope.Bound ||
				report.ReleaseScope.BaselineMatchesStable && report.ReleaseScope.CandidateMatchesPreview ||
				report.ReleaseScope.HealthyAffectedRoutes != 0 || report.ReleaseScope.RegressedAffectedRoutes != 0 ||
				report.ReleaseScope.InsufficientAffectedRoutes != report.ReleaseScope.AffectedExistingRoutes {
				t.Fatalf("mismatched release binding was scored: %+v", report)
			}
			for _, route := range report.Routes {
				if route.Status != "insufficient_evidence" || route.Evidence != nil || route.Coverage != "unknown" {
					t.Fatalf("mismatched route was scored: %+v", route)
				}
			}
		})
	}
}

func TestBuildReleaseRouteHealthReviewSurfacesUnselectableAffectedRoute(t *testing.T) {
	health, preview := releaseRouteHealthReviewFixture()
	for i := range preview.Routes {
		if preview.Routes[i].Path == "/sleepy" {
			preview.Routes[i].Path = "/sleepy/*"
		}
	}
	report, err := buildReleaseRouteHealthReviewReport(health, "api", healthCandidateID, preview)
	if err != nil {
		t.Fatal(err)
	}
	if report.ReleaseScope.AffectedExistingRoutes != 3 || report.ReleaseScope.UnselectableAffectedRoutes != 1 ||
		report.ReleaseScope.InsufficientAffectedRoutes != 1 || len(report.Routes) != 3 {
		t.Fatalf("unselectable route was dropped from coverage: %+v", report)
	}
	for _, route := range report.Routes {
		if route.Path == "/sleepy/*" && (route.Status != "insufficient_evidence" || route.Reason != "route_cannot_be_selected_uniquely") {
			t.Fatalf("unselectable route details = %+v", route)
		}
	}
}

func TestReleaseRouteHealthGateRequiresCompleteImpactAndHealthyCoverage(t *testing.T) {
	health, preview := releaseRouteHealthReviewFixture()
	preview.SourceImpact.AnalysisStatus = "complete"
	preview.SourceImpact.MappingStatus = "complete"
	preview.SourceImpact.IssueCount = 0
	preview.SourceImpact.Unmatched = []previewUnmatchedSource{}
	kept := preview.Routes[:0]
	for _, route := range preview.Routes {
		if route.Path != "/old" && route.Path != "/new" {
			kept = append(kept, route)
		}
	}
	preview.Routes = kept
	for i := range health.Routes {
		if health.Routes[i].Path == "/users/{id}" {
			for j := range health.Routes[i].Windows {
				health.Routes[i].Windows[j].Candidate.ServerErrors = 0
			}
		}
	}
	sleepy := api.RouteHealthFinding{Method: "GET", Path: "/sleepy", Windows: routehealth.Windows(health.CheckedAt)}
	for i := range sleepy.Windows {
		sleepy.Windows[i].Candidate.Requests = 100
		sleepy.Windows[i].Stable.Requests = 100
	}
	health.Routes = append(health.Routes, sleepy)
	anchor := health.CheckedAt.Add(-time.Hour)
	routehealth.Evaluate(&health, &anchor, "")
	if health.Status != "healthy" {
		t.Fatalf("fixture candidate status = %s", health.Status)
	}
	report, err := buildReleaseRouteHealthReviewReport(health, "api", healthCandidateID, preview)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "healthy" || report.ReleaseGate.Status != "ready" || len(report.ReleaseGate.Reasons) != 0 ||
		report.ReleaseScope.AffectedExistingRoutes != 3 || report.ReleaseScope.SelectedAffectedRoutes != 3 {
		t.Fatalf("complete, healthy release did not pass: %+v", report)
	}
}

func TestRoutesHealthReviewReadsBoundPreviewAndCandidateReport(t *testing.T) {
	resetJSONOut(t)
	setPreviewTestAuth(t)
	health, preview := releaseRouteHealthReviewFixture()
	previewBody, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	previewPath := filepath.Join(t.TempDir(), "preview-report.json")
	if err := os.WriteFile(previewPath, previewBody, 0o600); err != nil {
		t.Fatal(err)
	}
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/route-health/deployments/"+healthCandidateID ||
			r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected route health request: %s %s", r.Method, r.URL.String())
		}
		writeJSONTest(w, health)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)

	var out bytes.Buffer
	oldOut, oldJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	code := cmdRoutesHealth([]string{"review", "api", "--deployment", healthCandidateID, "--preview-report", previewPath})
	if code != 0 {
		t.Fatalf("review returned %d: %s", code, out.String())
	}
	if reads != 1 {
		t.Fatalf("route health reads = %d, want one", reads)
	}
	var report routeHealthReviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode review: %v (%s)", err, out.String())
	}
	if report.Status != "regressed" || report.ReleaseGate.Status != "not_ready" || report.ReleaseScope.Preview != "pr-42-api" || len(report.Routes) != 3 {
		t.Fatalf("unexpected review report: %+v", report)
	}
	for _, id := range []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"} {
		if strings.Contains(out.String(), id) {
			t.Fatalf("customer identity leaked in CLI report: %s", out.String())
		}
	}
	out.Reset()
	code = cmdRoutesHealth([]string{"review", "api", "--deployment", healthCandidateID, "--preview-report", previewPath, "--fail-on-incomplete"})
	if code != 1 {
		t.Fatalf("incomplete release returned %d, want a failing CI exit; output: %s", code, out.String())
	}
	if reads != 2 {
		t.Fatalf("route health reads = %d, want two", reads)
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.ReleaseGate.Status != "not_ready" || len(report.ReleaseGate.Reasons) == 0 {
		t.Fatalf("missing machine-readable failed gate report: %v (%s)", err, out.String())
	}
}
