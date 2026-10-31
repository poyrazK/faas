package dashboard

import (
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCPUProfilesRenderEscapesSymbolsAndShowsMissingData(t *testing.T) {
	query := api.ProfileQuery{DeploymentID: "candidate", Runtime: "node24", Start: time.Now().Add(-time.Minute), End: time.Now()}
	data := struct {
		RouteView       *ProfileRouteView
		CanaryFinding   any
		AppSlug         string
		Deployments     []struct{ ID, CommitSHA string }
		Query, Baseline api.ProfileQuery
		Profile         *api.ProfileResponse
		Compare         *api.ProfileCompareResponse
		Error           string
		Investigations  *ProfileInvestigationsView
		Automatic       *ProfileDeploymentChecksView
		CPUChart        *ProfileCPUChart
		Captures        *ProfileCapturesView
	}{AppSlug: "profile-app", Query: query}
	data.Profile = &api.ProfileResponse{Query: query, Empty: true}
	data.CPUChart = &ProfileCPUChart{Points: []ProfileCPUPoint{{URL: "/dashboard/apps/profile-app/profiles?deployment_id=candidate&runtime=node24", Cores: 1, Height: 140, Width: 30}}}
	data.Profile.Coverage = &api.ProfileCoverage{Available: true, ReceivedProfiles: 3, ContributingCollectors: 2, WindowSeconds: 60, CoveredSeconds: 20, GapSeconds: 40, RecordedFailedUploads: 1}
	rec := httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "test-nonce", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "Missing samples do not mean zero CPU usage") {
		t.Fatal("missing data shown as measured zero")
	}
	for _, want := range []string{"App CPU — all deployments", "Collection coverage", "At least 1", "20.000 of 60.000", "runtime=node24"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatal("missing profiling evidence", want)
		}
	}
	data.Profile.Coverage.Available = false
	malicious := "</script><script>alert(1)</script>"
	data.Profile.Empty = false
	data.Profile.Functions = []api.ProfileFunction{{Name: malicious}}
	data.Profile.Flamegraph = &api.ProfileStack{Name: malicious, CPUSeconds: 1}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "test-nonce", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	body := rec.Body.String()
	if strings.Contains(body, malicious) || !strings.Contains(body, `\u003c/script\u003e`) {
		t.Fatal("profile symbols escaped incorrectly in JavaScript")
	}
	if !strings.Contains(body, `nonce="test-nonce"`) {
		t.Fatal("missing CSP nonce")
	}
	if !strings.Contains(body, "Collection coverage is unavailable") {
		t.Fatal("unknown coverage displayed as zero")
	}
	data.Compare = &api.ProfileCompareResponse{Comparable: true, Baseline: api.ProfileResponse{Coverage: &api.ProfileCoverage{Available: true, CoveredSeconds: 20, WindowSeconds: 60}}, Candidate: *data.Profile}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "test-nonce", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "Baseline collection: 20.000 of 60.000") || !strings.Contains(rec.Body.String(), "Candidate collection: unavailable") {
		t.Fatal("comparison concealed missing coverage")
	}
	rate, zero := 1.0, 0.0
	data.Compare.Flamegraph = &api.ProfileStackDelta{Name: malicious, WidthCPUPerSecond: 2, BaselineCPUPerSecond: &rate, CandidateCPUPerSecond: &rate, DeltaCPUPerSecond: &zero, Children: []*api.ProfileStackDelta{}}
	data.Compare.Functions = []api.ProfileFunctionDelta{{Name: "unobserved baseline", CandidateObserved: true, CandidateCPUPerSecond: 1}}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "test-nonce", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	body = rec.Body.String()
	if strings.Contains(body, malicious) || !strings.Contains(body, `\u003c/script\u003e`) || !strings.Contains(body, "Differential flamegraph") || !strings.Contains(body, "Not observed") || !strings.Contains(body, ">Unknown<") {
		t.Fatal("differential symbols or unobserved function rates rendered incorrectly")
	}
	data.Investigations = &ProfileInvestigationsView{CanSave: true, CSRF: "csrf-fixture", TitleMaxBytes: api.ProfileInvestigationMaxTitleBytes, TextMaxBytes: api.ProfileInvestigationMaxTextBytes}
	data.Investigations.Saved = &api.ProfileInvestigationResponse{Saved: api.ProfileInvestigation{ID: "saved", Revision: 3, Investigation: api.ProfileInvestigationInput{Title: malicious, Findings: malicious, Notes: "</textarea><script>alert(2)</script>"}}, URL: "/dashboard/apps/profile-app/profiles?investigation_id=saved"}
	data.Investigations.SelectedPath = &api.ProfileCallPath{View: "comparison", Frames: []api.ProfileCallPathFrame{{Name: malicious}}}
	data.Investigations.Saved.Saved.Assessment = &api.ProfileRegressionAssessment{InvestigationRevision: 2, Status: "regressed", Reason: malicious, Options: api.DefaultProfileRegressionOptions(), Evidence: []api.ProfileRegressionEvidence{{Kind: "call_path", Frames: []api.ProfileCallPathFrame{{Name: malicious, File: malicious}}}}}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "test-nonce", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	body = rec.Body.String()
	if strings.Contains(body, malicious) || strings.Contains(body, "</textarea><script>alert(2)</script>") || !strings.Contains(body, `name="expected_revision" value="3"`) || !strings.Contains(body, `name="csrf_token" value="csrf-fixture"`) {
		t.Fatal("saved investigation commentary, path or form protection rendered incorrectly")
	}
	if !strings.Contains(body, `id="regression-stale"`) || !strings.Contains(body, `id="regression-evidence"`) || !strings.Contains(body, `name="minimum_coverage_ratio"`) || !strings.Contains(body, `#diff-flamegraph`) {
		t.Fatal("assessment evidence, stale state, thresholds or comparison link missing")
	}

	data.Automatic = &ProfileDeploymentChecksView{CanEdit: true, CSRF: "periodic-csrf", Policy: api.ProfileDeploymentPolicy{Config: api.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: "node24", WindowSeconds: 300, Options: api.DefaultProfileRegressionOptions(), Periodic: &api.PeriodicProfilePolicy{IntervalSeconds: 900, Confirmations: 2}}}, Monitors: []api.ProfilePeriodicMonitor{{DeploymentID: "candidate", Route: "POST /checkout", Baseline: &query, History: []api.ProfilePeriodicObservation{{CheckedAt: time.Now(), Status: "regressed", Reason: malicious, IncidentID: "incident", Transition: "profile.route_regressed", ComparisonURL: "/dashboard/apps/profile-app/profiles?investigation_id=saved"}}}}}
	rec = httptest.NewRecorder()
	if err := Render(rec, slog.Default(), "test-nonce", Page{Body: "app_profiles", Data: data}); err != nil {
		t.Fatal(err)
	}
	body = rec.Body.String()
	if strings.Contains(body, malicious) {
		t.Fatal("periodic history reason was not escaped")
	}
	for _, want := range []string{`name="periodic_enabled" checked`, `name="periodic_confirmations"`, `value="900"`, "Periodic route incident history", "Monitor inactive", "profile.route_regressed", "Open profile comparison"} {
		if !strings.Contains(body, want) {
			t.Fatal("periodic policy or history missing", want)
		}
	}

}
