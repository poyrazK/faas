package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
)

func readyReleaseRouteHealthFixture() (api.RouteHealthReport, main.PreviewRouteReport) {
	health, preview := releaseRouteHealthReviewFixture()
	preview.SourceImpact.AnalysisStatus = "complete"
	preview.SourceImpact.MappingStatus = "complete"
	preview.SourceImpact.IssueCount = 0
	preview.SourceImpact.Unmatched = []main.PreviewUnmatchedSource{}
	preview.Routes = slices.DeleteFunc(preview.Routes, func(route main.PreviewReportRoute) bool {
		return route.Path == "/old" || route.Path == "/new"
	})
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
	return health, preview
}

func releaseHealthAggregateFixture(preview main.PreviewRouteReport) main.PreviewReleaseRouteReview {
	preview.CandidateDeployment = healthCandidateID
	return main.PreviewReleaseRouteReview{
		Version: 1, GeneratedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), Outcome: "review_required",
		Summary:  main.PreviewReleaseRouteReviewSummary{PreviewCount: 1, AvailablePreviews: 1},
		Previews: []main.PreviewReleaseRouteReviewPreview{{Slug: preview.Preview, Status: "available", Report: &preview}},
	}
}

func TestRoutesHealthReviewReleasePassesCompleteHealthyAggregate(t *testing.T) {
	main.ResetJSONOut(t)
	main.SetPreviewTestAuth(t)
	health, preview := readyReleaseRouteHealthFixture()
	release := releaseHealthAggregateFixture(preview)
	body, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	releasePath := filepath.Join(t.TempDir(), "release-review.json")
	if err := os.WriteFile(releasePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/api/route-health/deployments/"+healthCandidateID ||
			r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("unexpected route health request: %s %s", r.Method, r.URL.String())
		}
		main.WriteJSONTest(w, health)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)

	var out bytes.Buffer
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	(*main.OsStdout), (*main.JsonOutput) = &out, true
	t.Cleanup(func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON })
	code := main.CmdRoutesHealth([]string{"review-release", "--release-report", releasePath, "--fail-on-incomplete"})
	if code != 0 {
		t.Fatalf("release review returned %d: %s", code, out.String())
	}
	if reads != 1 {
		t.Fatalf("route health reads = %d, want one", reads)
	}
	var report main.RouteHealthReleaseReviewReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatalf("decode release review: %v (%s)", err, out.String())
	}
	if report.Version != 1 || report.ReleaseGate.Status != "ready" || len(report.ReleaseGate.Reasons) != 0 ||
		len(report.Previews) != 1 || report.Previews[0].App != "api" || report.Previews[0].Status != "ready" ||
		report.Previews[0].Report == nil || report.Previews[0].Report.ReleaseScope.AffectedExistingRoutes != 3 {
		t.Fatalf("unexpected release health report: %+v", report)
	}
}

func TestRoutesHealthReviewReleaseFailsClosedForUnavailablePreview(t *testing.T) {
	release := releaseHealthAggregateFixture(main.PreviewRouteReport{
		Version: 7, Preview: "pr-42-api", Parent: "api", BaselineDeployment: main.CustomerBaseline,
		GeneratedAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), Routes: []main.PreviewReportRoute{},
	})
	release.Previews = append(release.Previews, main.PreviewReleaseRouteReviewPreview{
		Slug: "pr-42-worker", Status: "unavailable", Reason: "preview_or_evidence_unavailable",
	})
	release.Summary = main.PreviewReleaseRouteReviewSummary{PreviewCount: 2, AvailablePreviews: 1, UnavailablePreviews: 1}
	if err := main.ValidateRouteHealthReleasePreviewInput(release); err != nil {
		t.Fatalf("validate aggregate: %v", err)
	}
	result := main.CollectRouteHealthReleaseReview(context.Background(), nil, release)
	if result.ReleaseGate.Status != "not_ready" || !slices.Contains(result.ReleaseGate.Reasons, "api:source_impact_unavailable") ||
		!slices.Contains(result.ReleaseGate.Reasons, "preview[pr-42-worker]:preview_unavailable") {
		t.Fatalf("release gate did not preserve scoped failures: %+v", result.ReleaseGate)
	}
}

func TestReadPreviewReleaseRouteReviewRejectsUnknownFieldsAndDuplicatePreviews(t *testing.T) {
	_, preview := releaseRouteHealthReviewFixture()
	release := releaseHealthAggregateFixture(preview)
	release.Previews = append(release.Previews, release.Previews[0])
	release.Summary = main.PreviewReleaseRouteReviewSummary{PreviewCount: 2, AvailablePreviews: 2}
	body, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "duplicate.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ReadPreviewReleaseRouteReview(path); err == nil || !strings.Contains(err.Error(), "duplicate preview slugs") {
		t.Fatalf("duplicate previews accepted: %v", err)
	}

	unknown := strings.Replace(string(body), `"version":1`, `"version":1,"unexpected":true`, 1)
	if err := os.WriteFile(path, []byte(unknown), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ReadPreviewReleaseRouteReview(path); err == nil {
		t.Fatal("unknown aggregate field accepted")
	}
}
