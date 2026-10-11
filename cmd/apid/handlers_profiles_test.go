package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

type profileQueryCapture struct {
	queries            int
	tenant, app, scope string
	profile            *profile.Profile
	profiles           map[string]*profile.Profile
}

type profileCoverageCapture struct {
	profileQueryCapture
	unavailable     bool
	coverageQueries int
}

func (b *profileCoverageCapture) QueryCoverage(_ context.Context, tenant, app, scope string, q api.ProfileQuery) (api.ProfileCoverage, error) {
	b.coverageQueries++
	if b.unavailable {
		return api.ProfileCoverage{}, fmt.Errorf("coverage unavailable")
	}
	if tenant != b.tenant || app != b.app || scope != b.scope {
		return api.ProfileCoverage{}, fmt.Errorf("coverage identity mismatch")
	}
	return api.ProfileCoverage{Available: true, ReceivedProfiles: 2, ContributingCollectors: 2, WindowSeconds: q.End.Sub(q.Start).Seconds(), CoveredSeconds: 1, RecordedFailedUploads: 1}, nil
}

func TestCPUProfileCoverageFailurePreservesCPUResponse(t *testing.T) {
	e := setup(t, api.PlanHobby)
	backend := &profileCoverageCapture{}
	e.s.profileBackend = backend
	e.s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "coverage-owned", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"deployment_id": {dep.ID}, "runtime": {"node24"}, "start": {time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}, "end": {time.Now().Format(time.RFC3339Nano)}}
	for _, unavailable := range []bool{false, true} {
		backend.unavailable = unavailable
		response := e.do(t, "GET", "/v1/apps/"+app.Slug+"/profiles?"+values.Encode(), nil, nil)
		var out api.ProfileResponse
		if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || !out.Empty || out.Coverage == nil || out.Coverage.Available == unavailable {
			t.Fatal("coverage changed CPU availability", response.Code, response.Body.String())
		}
	}
}

func TestCPUProfileDashboardIntervalScopesQueryAndPreservesOverview(t *testing.T) {
	e := setup(t, api.PlanHobby)
	backend := &profileCoverageCapture{}
	e.s.profileBackend = backend
	e.s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "cpu-drilldown", Runtime: "node24", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	cpuQueries := 0
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cpuQueries++
		query := r.URL.Query().Get("query")
		if !strings.Contains(query, fmt.Sprintf("app=%q", app.ID)) || strings.Contains(query, "forged-app") {
			t.Error("CPU query lost app scope", query)
		}
		_, _ = fmt.Fprintf(w, `{"data":{"resultType":"matrix","result":[{"metric":{},"values":[[%d,"0.2"],[%d,"1.5"]]}]}}`, now.Add(-time.Minute).Unix(), now.Unix())
	}))
	defer prom.Close()
	e.s.promqlClient = promql.NewClient(prom.URL, prom.Client())
	values := url.Values{"deployment_id": {dep.ID}, "runtime": {"node24"}, "start": {now.Add(-5 * time.Minute).Format(time.RFC3339Nano)}, "end": {now.Format(time.RFC3339Nano)}, "app_id": {"forged-app"}}
	request := httptest.NewRequest("GET", "/dashboard/apps/"+app.Slug+"/profiles?"+values.Encode(), nil)
	response := httptest.NewRecorder()
	e.s.renderAppProfiles(response, request, e.s.log, e.acct, app.Slug)
	if response.Code != 200 || cpuQueries != 1 || !strings.Contains(response.Body.String(), "App CPU — all deployments") {
		t.Fatal(response.Code, response.Body.String())
	}
	links := regexp.MustCompile(`<a href="([^"]+)" tabindex="0"`).FindAllStringSubmatch(response.Body.String(), -1)
	if len(links) != 2 {
		t.Fatal("CPU intervals are not links", response.Body.String())
	}
	link, err := url.Parse(html.UnescapeString(links[1][1]))
	if err != nil {
		t.Fatal(err)
	}
	q := link.Query()
	if q.Get("deployment_id") != dep.ID || q.Get("runtime") != "node24" || q.Get("start") != now.Add(-time.Minute).Format(time.RFC3339Nano) || q.Get("chart_start") != values.Get("start") || q.Get("chart_end") != values.Get("end") {
		t.Fatal("interval lost scope or overview", q)
	}
	q.Set("chart_start", "invalid")
	request = httptest.NewRequest("GET", link.Path+"?"+q.Encode(), nil)
	response = httptest.NewRecorder()
	e.s.renderAppProfiles(response, request, e.s.log, e.acct, app.Slug)
	if cpuQueries != 1 || !strings.Contains(response.Body.String(), "Enter valid CPU chart timestamps") {
		t.Fatal("invalid overview reached Prometheus")
	}
	request = httptest.NewRequest("GET", "/dashboard/apps/"+app.Slug+"/profiles?"+values.Encode(), nil)
	response = httptest.NewRecorder()
	foreign := e.acct
	foreign.ID = "foreign-account"
	e.s.renderAppProfiles(response, request, e.s.log, foreign, app.Slug)
	if response.Code != 404 || cpuQueries != 1 {
		t.Fatal("foreign owner reached CPU chart")
	}
}

func (*profileQueryCapture) Push(context.Context, profiling.Principal, *profile.Profile) error {
	return nil
}
func (b *profileQueryCapture) Query(_ context.Context, tenant, app, scope string, q api.ProfileQuery) (*profile.Profile, error) {
	b.queries++
	b.tenant, b.app, b.scope = tenant, app, scope
	if b.profiles != nil {
		if p := b.profiles[q.DeploymentID]; p != nil {
			return p.Copy(), nil
		}
		return nil, nil
	}
	if b.profile != nil {
		return b.profile.Copy(), nil
	}
	return nil, nil
}

func TestCPUProfileDifferentialCompareEndpoint(t *testing.T) {
	e := setup(t, api.PlanHobby)
	backend := &profileQueryCapture{profiles: map[string]*profile.Profile{}}
	e.s.profileBackend = backend
	e.s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "differential-owned", Type: state.AppTypeFunction, Runtime: "node24", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	makeProfile := func(cpu, line int64) *profile.Profile {
		fn := &profile.Function{ID: 1, Name: "hot", Filename: "/app/server.js"}
		location := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: line}}}
		return &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Function: []*profile.Function{fn}, Location: []*profile.Location{location}, Sample: []*profile.Sample{{Location: []*profile.Location{location}, Value: []int64{cpu}}}}
	}
	now := time.Now()
	request := api.ProfileCompareRequest{}
	for i, query := range []*api.ProfileQuery{&request.Baseline, &request.Candidate} {
		sha := strings.Repeat(string(rune('a'+i)), 40)
		dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub, SourceURL: "github://acme/service@" + sha, CommitSHA: sha, SourceRoot: "apps/api", Scope: "prod"})
		if err != nil {
			t.Fatal(err)
		}
		*query = api.ProfileQuery{DeploymentID: dep.ID, Runtime: "node24", Start: now.Add(-time.Duration((i+1)*10) * time.Second), End: now}
		backend.profiles[dep.ID] = makeProfile(int64(1+i*5)*1e9, int64(10+i*20))
	}
	response := e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/profiles/compare", request, nil)
	var out api.ProfileCompareResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || !out.Comparable || out.Flamegraph == nil || len(out.Flamegraph.Children) != 1 || backend.queries != 2 || backend.tenant != e.acct.ID || backend.app != app.ID || backend.scope != "prod" {
		t.Fatal("differential response lost scope or call paths", response.Code, response.Body.String())
	}
	frame := out.Flamegraph.Children[0]
	if frame.DeltaCPUPerSecond == nil || *frame.DeltaCPUPerSecond < .199999 || *frame.DeltaCPUPerSecond > .200001 || frame.BaselineLine != 10 || frame.CandidateLine != 30 || frame.BaselineSource == nil || frame.CandidateSource == nil || !strings.Contains(frame.BaselineSource.URL, strings.Repeat("a", 40)) || !strings.Contains(frame.CandidateSource.URL, strings.Repeat("b", 40)) {
		t.Fatal("normalized delta or revision-specific source incorrect", frame)
	}
	backend.profiles[request.Candidate.DeploymentID] = nil
	response = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/profiles/compare", request, nil)
	// Decode into a fresh value: an omitted optional tree must stay absent.
	var missing api.ProfileCompareResponse
	if err := json.Unmarshal(response.Body.Bytes(), &missing); err != nil || response.Code != 200 || missing.Comparable || missing.Flamegraph != nil || missing.Reason == "" {
		t.Fatal("missing profile produced a measured delta", response.Body.String(), err)
	}
	other, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "differential-other", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: other.ID, SourceURL: "github://secret/repo@" + strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	request.Candidate.DeploymentID = foreign.ID
	response = e.do(t, http.MethodPost, "/v1/apps/"+app.Slug+"/profiles/compare", request, nil)
	if response.Code != 404 || backend.queries != 5 || strings.Contains(response.Body.String(), "secret/repo") {
		t.Fatal("comparison queried an unrelated deployment", response.Code, response.Body.String(), backend.queries)
	}
}

func TestCPUProfileSourceUsesOwnedDeploymentProvenance(t *testing.T) {
	e := setup(t, api.PlanHobby)
	fn := &profile.Function{ID: 1, Name: "hot", Filename: "/app/server.js"}
	location := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 12}}}
	backend := &profileQueryCapture{profile: &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Function: []*profile.Function{fn}, Location: []*profile.Location{location}, Sample: []*profile.Sample{{Location: []*profile.Location{location}, Value: []int64{1e8}}}}}
	e.s.profileBackend = backend
	e.s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "source-owned", RootDir: "new-root", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	buildProfile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: "node"})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Kind: state.DeploymentKindGitHub, SourceURL: "github://acme/old-repo@" + sha, CommitSHA: sha, SourceRoot: "old-root", InferredProfile: buildProfile})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"deployment_id": {dep.ID}, "runtime": {"node24"}, "start": {time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}, "end": {time.Now().Format(time.RFC3339Nano)}, "source_url": {"https://evil.test"}, "commit_sha": {strings.Repeat("b", 40)}, "source_root": {"forged-root"}}
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/profiles?"+values.Encode(), nil, nil)
	var out api.ProfileResponse
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	want := "https://github.com/acme/old-repo/blob/" + sha + "/old-root/server.js#L12"
	if response.Code != 200 || out.Source == nil || !out.Source.Available || len(out.Functions) != 1 || out.Functions[0].Source == nil || out.Functions[0].Source.URL != want || out.Flamegraph.Children[0].Source.URL != want {
		t.Fatal("source did not use the frozen owned deployment", response.Code, response.Body.String())
	}
	other, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "source-other", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	response = e.do(t, http.MethodGet, "/v1/apps/"+other.Slug+"/profiles?"+values.Encode(), nil, nil)
	if response.Code != 404 || backend.queries != 1 || strings.Contains(response.Body.String(), "old-repo") {
		t.Fatal("another app exposed deployment source", response.Code, response.Body.String())
	}
}

func TestCPUProfileSourceLayoutRequiresKnownBuild(t *testing.T) {
	for _, tc := range []struct {
		name       string
		kind       state.DeploymentKind
		framework  string
		function   bool
		dockerfile string
		want       bool
	}{
		{"managed source", state.DeploymentKindGitHub, "node", false, "", true},
		{"GitHub Dockerfile", state.DeploymentKindGitHub, "docker", false, "", false},
		{"explicit Dockerfile", state.DeploymentKindDockerfile, "node", false, "", false},
		{"profile Dockerfile", state.DeploymentKindGitHub, "node", false, "deploy/Dockerfile", false},
		{"image", state.DeploymentKindImage, "node", false, "", false},
		{"missing old profile", state.DeploymentKindGitHub, "", false, "", false},
		{"GitHub function without handler field", state.DeploymentKindGitHub, "", true, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := state.App{Type: state.AppTypeApp}
			if tc.function {
				app.Type = state.AppTypeFunction
			}
			profile, err := json.Marshal(frameworkprofile.Profile{Version: frameworkprofile.Version, Framework: tc.framework, DockerfilePath: tc.dockerfile})
			if err != nil {
				t.Fatal(err)
			}
			origin := profileSourceProvenance(app, state.Deployment{Kind: tc.kind, InferredProfile: profile})
			if origin.ManagedPaths != tc.want || origin.Function != tc.function {
				t.Fatal("incorrect source mapping", origin)
			}
		})
	}
}

func TestCPUProfileAPIIsolationAndMissingSamples(t *testing.T) {
	e := setup(t, api.PlanHobby)
	backend := &profileQueryCapture{}
	e.s.profileBackend = backend
	e.s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "profile-owned", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: app.ID, Scope: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"deployment_id": {dep.ID}, "runtime": {"go124"}, "start": {time.Now().Add(-time.Minute).Format(time.RFC3339Nano)}, "end": {time.Now().Format(time.RFC3339Nano)}, "account_id": {"forged"}}
	response := e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/profiles?"+values.Encode(), nil, nil)
	if response.Code != 200 || backend.queries != 1 || backend.tenant != e.acct.ID || backend.app != app.ID || backend.scope != "prod" {
		t.Fatal("owned query failed", response.Code, response.Body.String())
	}
	if response.Body.String() == "" {
		t.Fatal("empty response instead of empty profile")
	}
	other, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "profile-other", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.store.CreateDeployment(t.Context(), state.Deployment{AppID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	values.Set("deployment_id", foreign.ID)
	response = e.do(t, http.MethodGet, "/v1/apps/"+app.Slug+"/profiles?"+values.Encode(), nil, nil)
	if response.Code != 404 || backend.queries != 1 {
		t.Fatal("another app deployment reached backend", response.Code)
	}
	another, err := e.store.CreateAccount(t.Context(), "profile-other@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	stolen, err := e.store.CreateApp(t.Context(), state.App{AccountID: another.ID, Slug: "profile-stolen", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	response = e.do(t, http.MethodGet, "/v1/apps/"+stolen.Slug+"/profiles?"+values.Encode(), nil, nil)
	if response.Code != 404 || backend.queries != 1 {
		t.Fatal("cross-account query reached backend", response.Code)
	}
}

func TestCPUProfileAPIPlanAndInstallationGates(t *testing.T) {
	e := setup(t, api.PlanFree)
	response := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "profile-free", Profiling: &api.ProfilingConfig{Enabled: true}}, nil)
	if response.Code < 400 {
		t.Fatal("enabled profiling accepted without operator backend")
	}
	e.s.profileBackend = &profileQueryCapture{}
	response = e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "profile-free", Profiling: &api.ProfilingConfig{Enabled: true}}, nil)
	if response.Code < 400 {
		t.Fatal("Free enabled profiling accepted")
	}
}
