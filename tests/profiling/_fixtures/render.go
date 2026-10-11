// Render a deterministic dashboard fixture for the browser smoke test.
package main

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/profiling"
)

func main() {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	q := api.ProfileQuery{DeploymentID: "11111111-1111-4111-8111-111111111111", Runtime: "node24", Start: now.Add(-time.Hour), End: now}
	data := struct {
		AppSlug         string
		Deployments     []struct{ ID, CommitSHA string }
		Query, Baseline api.ProfileQuery
		Profile         *api.ProfileResponse
		Compare         *api.ProfileCompareResponse
		Error           string
		Investigations  *dashboard.ProfileInvestigationsView
		Automatic       *dashboard.ProfileDeploymentChecksView
		RouteView       *dashboard.ProfileRouteView
		CanaryFinding   any
		CPUChart        *dashboard.ProfileCPUChart
	}{AppSlug: "profile-demo", Query: q}
	data.Deployments = append(data.Deployments, struct{ ID, CommitSHA string }{q.DeploymentID, "demo-candidate"}, struct{ ID, CommitSHA string }{"22222222-2222-4222-8222-222222222222", "demo-baseline"})
	data.CPUChart = &dashboard.ProfileCPUChart{Start: q.Start.Format(time.RFC3339), End: q.End.Format(time.RFC3339), PeakCores: 1.5}
	for i := range 60 {
		cores := .15
		if i > 25 && i < 34 {
			cores = 1.5
		}
		start := q.Start.Add(time.Duration(i) * time.Minute)
		data.CPUChart.Points = append(data.CPUChart.Points, dashboard.ProfileCPUPoint{X: float64(i) * 1100 / 60, Width: 1100.0/60 - 1, Height: cores / 1.5 * 140, Y: 150 - cores/1.5*140, Cores: cores, Start: start.Format(time.RFC3339), End: start.Add(time.Minute).Format(time.RFC3339), URL: "/dashboard/apps/profile-demo/profiles?deployment_id=" + q.DeploymentID + "&runtime=node24&start=" + start.Format(time.RFC3339) + "&end=" + start.Add(time.Minute).Format(time.RFC3339) + "&chart_start=" + data.CPUChart.Start + "&chart_end=" + data.CPUChart.End})
	}
	data.Profile = &api.ProfileResponse{Query: q, CPUSeconds: 10, StackCount: 2, Functions: []api.ProfileFunction{{Name: "parseJSON", File: "app.js", Line: 24, SelfCPUSeconds: 8.4, TotalCPUSeconds: 8.4}, {Name: "handler", File: "app.js", Line: 5, SelfCPUSeconds: 1.6, TotalCPUSeconds: 10}}, Flamegraph: &api.ProfileStack{Name: "all", CPUSeconds: 10, Children: []*api.ProfileStack{{Name: "handler", CPUSeconds: 10, Children: []*api.ProfileStack{{Name: "parseJSON", CPUSeconds: 8.4}}}}}, Coverage: &api.ProfileCoverage{Available: true, ReceivedProfiles: 12, ContributingCollectors: 2, LastReceivedAt: &now, WindowSeconds: 3600, CoveredSeconds: 600, GapSeconds: 3000, RecordedFailedUploads: 1}}
	data.Profile.Flamegraph.Children[0].File, data.Profile.Flamegraph.Children[0].Line = "app.js", 5
	data.Profile.Flamegraph.Children[0].Children[0].File, data.Profile.Flamegraph.Children[0].Children[0].Line = "app.js", 24
	data.Profile.Flamegraph.Children[0].Children = append(data.Profile.Flamegraph.Children[0].Children,
		&api.ProfileStack{Name: "cacheLookup", File: "app.js", Line: 30, CPUSeconds: 1},
		&api.ProfileStack{Name: "newPath", File: "app.js", Line: 42, CPUSeconds: .2})
	data.Profile.Functions[1].SelfCPUSeconds = .4
	data.Profile.Functions = append(data.Profile.Functions, api.ProfileFunction{Name: "cacheLookup", File: "app.js", Line: 30, SelfCPUSeconds: 1, TotalCPUSeconds: 1}, api.ProfileFunction{Name: "newPath", File: "app.js", Line: 42, SelfCPUSeconds: .2, TotalCPUSeconds: .2})
	const candidateSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const baselineSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	data.Deployments[0].CommitSHA, data.Deployments[1].CommitSHA = candidateSHA, baselineSHA
	profiling.LinkSources(data.Profile, profiling.SourceProvenance{SourceURL: "github://acme/profile-demo@" + candidateSHA, CommitSHA: candidateSHA, SourceRoot: "apps/api", ManagedPaths: true})
	baseline := api.ProfileResponse{Query: q, Functions: []api.ProfileFunction{{Name: "parseJSON", File: "app.js", Line: 18, SelfCPUSeconds: 4}, {Name: "handler", File: "app.js", Line: 5, SelfCPUSeconds: 1}}, Coverage: data.Profile.Coverage}
	baseline.CPUSeconds = 10
	baseline.Functions[1].SelfCPUSeconds = .5
	baseline.Functions = append(baseline.Functions, api.ProfileFunction{Name: "cacheLookup", File: "app.js", Line: 27, SelfCPUSeconds: 5}, api.ProfileFunction{Name: "legacyPath", File: "app.js", Line: 8, SelfCPUSeconds: .5})
	baseline.Flamegraph = &api.ProfileStack{Name: "all", CPUSeconds: 10, Children: []*api.ProfileStack{{Name: "handler", File: "app.js", Line: 5, CPUSeconds: 10, Children: []*api.ProfileStack{{Name: "parseJSON", File: "app.js", Line: 18, CPUSeconds: 4}, {Name: "cacheLookup", File: "app.js", Line: 27, CPUSeconds: 5}, {Name: "legacyPath", File: "app.js", Line: 8, CPUSeconds: .5}}}}}
	baseline.Query.DeploymentID = data.Deployments[1].ID
	baseline.Query.Start, baseline.Query.End = q.Start.Add(-time.Hour), q.Start
	data.Baseline = baseline.Query
	profiling.LinkSources(&baseline, profiling.SourceProvenance{SourceURL: "github://acme/profile-demo@" + baselineSHA, CommitSHA: baselineSHA, SourceRoot: "apps/api", ManagedPaths: true})
	if os.Getenv("GREGALE_PROFILE_FIXTURE") == "missing" {
		baseline.Empty = true
	}
	comparison := profiling.Compare(baseline, *data.Profile)
	data.Compare = &comparison

	data.Investigations = &dashboard.ProfileInvestigationsView{CanSave: true, CSRF: "fixture-csrf", TitleMaxBytes: api.ProfileInvestigationMaxTitleBytes, TextMaxBytes: api.ProfileInvestigationMaxTextBytes}
	mode := os.Getenv("GREGALE_PROFILE_FIXTURE")
	if mode == "automatic" {
		config := api.ProfileDeploymentPolicyConfig{Enabled: true, Runtime: "node24", WindowSeconds: api.ProfileAutoDefaultWindowSeconds, WarmupSeconds: api.ProfileAutoDefaultWarmupSeconds, Options: api.DefaultProfileRegressionOptions()}
		data.Automatic = &dashboard.ProfileDeploymentChecksView{CanEdit: true, CSRF: "fixture-policy-csrf", Policy: api.ProfileDeploymentPolicy{AppID: "app", Revision: 1, Config: config, UpdatedAt: &now}, Checks: []api.ProfileDeploymentCheck{
			{DeploymentID: q.DeploymentID, Scope: "prod", Status: "regressed", Reason: "parseJSON increased beyond both thresholds.", CompletedAt: &now, InvestigationID: "33333333-3333-4333-8333-333333333333", ComparisonURL: "/dashboard/apps/profile-demo/profiles?investigation_id=33333333-3333-4333-8333-333333333333#diff-flamegraph"},
			{DeploymentID: baseline.Query.DeploymentID, Scope: "staging", Status: "queued", Reason: "Waiting for the capture window and profile ingestion.", NextAttemptAt: &now},
		}}
	}
	if mode == "saved" || mode == "expired" || mode == "unobserved" || mode == "candidate-saved" || mode == "checked" || mode == "stale" || mode == "checked-expired" {
		path := &api.ProfileCallPath{View: "comparison", Frames: []api.ProfileCallPathFrame{{Name: "all"}, {Name: "handler", File: "app.js", Line: 5}, {Name: "parseJSON", File: "app.js", Line: 18}}}
		if mode == "candidate-saved" {
			path.View = "candidate"
			path.Frames[2].Line = 24
		}
		if mode == "unobserved" {
			path.Frames[2].Name = "removedFunction"
		}
		row := api.ProfileInvestigation{ID: "33333333-3333-4333-8333-333333333333", AppID: "44444444-4444-4444-8444-444444444444", Revision: 3, CreatedAt: now, UpdatedAt: now,
			Investigation: api.ProfileInvestigationInput{Title: "JSON parsing regression", Findings: "parseJSON increased", Notes: "Compare equivalent traffic before rollout", Baseline: baseline.Query, Candidate: q, SelectedPath: path}}
		out := api.ProfileInvestigationResponse{Saved: row, URL: "/dashboard/apps/profile-demo/profiles?investigation_id=" + row.ID, BaselineStatus: api.ProfileInvestigationWindowStatus{Status: "retained", Detail: "Window retained; samples may be absent."}, CandidateStatus: api.ProfileInvestigationWindowStatus{Status: "retained", Detail: "Window retained; samples may be absent."}}
		if mode == "checked" || mode == "stale" || mode == "checked-expired" {
			coverage := *data.Profile.Coverage
			coverage.CoveredSeconds, coverage.GapSeconds, coverage.RecordedFailedUploads = 3600, 0, 0
			data.Profile.Coverage, baseline.Coverage = &coverage, &coverage
			comparison.Baseline.Coverage, comparison.Candidate.Coverage = &coverage, &coverage
			options := api.DefaultProfileRegressionOptions()
			options.AbsoluteIncreaseCPUPerSecond = .001
			assessment := profiling.AssessRegression(profiling.NewRegressionAssessment(row, options, now), baseline, *data.Profile)
			out.Saved.Assessment, out.Saved.Revision = &assessment, assessment.InvestigationRevision
			if mode == "stale" {
				out.Saved.Revision++
			}
		}
		data.Investigations.Saved = &out
		data.Investigations.Items = []api.ProfileInvestigationResponse{out}
		data.Investigations.SelectedPath = path
		encoded, _ := json.Marshal(path)
		data.Investigations.SelectedPathJSON = string(encoded)
		if mode == "expired" || mode == "checked-expired" {
			out.BaselineStatus = api.ProfileInvestigationWindowStatus{Status: "expired", Detail: "This profile window has expired. Saved notes remain available."}
			out.CandidateStatus = out.BaselineStatus
			data.Investigations.Items = []api.ProfileInvestigationResponse{out}
			data.Profile = nil
			data.Compare = nil
			data.CPUChart = nil
			data.Error = out.BaselineStatus.Detail
		}
	}
	rec := httptest.NewRecorder()
	if err := dashboard.Render(rec, slog.Default(), "fixture-nonce", dashboard.Page{Body: "app_profiles", Title: "Profiling demo", Data: data}); err != nil {
		panic(err)
	}
	if _, err := rec.Body.WriteTo(os.Stdout); err != nil {
		panic(err)
	}
}
