package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
)

type regressionQueryCapture struct {
	profileQueryCapture
	baseline   api.ProfileQuery
	afterQuery func()
}

func (b *regressionQueryCapture) Query(ctx context.Context, tenant, app, scope string, q api.ProfileQuery) (*profile.Profile, error) {
	fn := &profile.Function{ID: 1, Name: "hot", Filename: "/app/server.js"}
	l := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 24}}}
	cpu := int64(40e9)
	if q.Start.Equal(b.baseline.Start) {
		cpu = 20e9
	}
	b.profile = &profile.Profile{SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Function: []*profile.Function{fn}, Location: []*profile.Location{l}, Sample: []*profile.Sample{{Location: []*profile.Location{l}, Value: []int64{cpu}}}}
	if b.afterQuery != nil {
		b.afterQuery()
	}
	return b.profileQueryCapture.Query(ctx, tenant, app, scope, q)
}

func (b *regressionQueryCapture) QueryCoverage(_ context.Context, _, _, _ string, q api.ProfileQuery) (api.ProfileCoverage, error) {
	s := q.End.Sub(q.Start).Seconds()
	return api.ProfileCoverage{Available: true, ReceivedProfiles: 10, ContributingCollectors: 1, WindowSeconds: s, CoveredSeconds: s}, nil
}

func TestCPUProfileRegressionAPIAndConcurrentEdit(t *testing.T) {
	e, app, req, _ := profileInvestigationFixture(t)
	req.Investigation.Candidate.Start = req.Investigation.Candidate.End.Add(-100 * time.Second)
	req.Investigation.Baseline = req.Investigation.Candidate
	req.Investigation.Baseline.Start = req.Investigation.Baseline.Start.Add(-100 * time.Second)
	req.Investigation.Baseline.End = req.Investigation.Baseline.End.Add(-100 * time.Second)
	backend := &regressionQueryCapture{baseline: req.Investigation.Baseline}
	e.s.profileBackend = backend
	row, err := e.store.SaveProfileInvestigation(t.Context(), e.acct.ID, app.ID, "", req)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/profiles/investigations/" + row.ID + "/check"
	check := api.CheckProfileRegressionRequest{ExpectedRevision: &row.Revision}
	if rec := e.do(t, "POST", path, map[string]any{"expected_revision": 1, "options": map[string]any{"absolute_increase_cpu_per_second": .01, "minimum_profiles": 3, "minimum_coverage_ratio": .8}}, nil); rec.Code != 400 {
		t.Fatal("partial threshold configuration accepted", rec.Code)
	}
	if rec := e.do(t, "POST", path, api.CheckProfileRegressionRequest{}, nil); rec.Code != 400 {
		t.Fatal("missing revision", rec.Code)
	}
	readKey, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "POST", path, check, map[string]string{"Authorization": "Bearer " + readKey}); rec.Code != 403 {
		t.Fatal("read key wrote assessment", rec.Code)
	}
	if rec := e.do(t, "POST", path, check, map[string]string{"Authorization": ""}); rec.Code != 401 {
		t.Fatal("unauthenticated check", rec.Code)
	}
	rec := e.do(t, "POST", path, check, nil)
	var out api.ProfileInvestigationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err, rec.Body.String())
	}
	if rec.Code != 200 || out.Saved.Revision != 2 || out.Saved.Assessment == nil || out.Saved.Assessment.Status != "regressed" || len(out.Saved.Assessment.Evidence) != 2 || backend.queries != 2 || backend.tenant != e.acct.ID || backend.app != app.ID || backend.scope != "prod" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "POST", path, check, nil); rec.Code != 409 || backend.queries != 2 {
		t.Fatal("stale check queried", rec.Code)
	}
	check.ExpectedRevision = &out.Saved.Revision
	backend.afterQuery = func() {
		backend.afterQuery = nil
		req.ExpectedRevision = &out.Saved.Revision
		req.Investigation.Notes = "Edited during check"
		if _, err := e.store.SaveProfileInvestigation(t.Context(), e.acct.ID, app.ID, row.ID, req); err != nil {
			t.Fatal(err)
		}
	}
	if rec := e.do(t, "POST", path, check, nil); rec.Code != 409 {
		t.Fatal("racing edit overwritten", rec.Code, rec.Body.String())
	}
	got, err := e.store.GetProfileInvestigation(t.Context(), e.acct.ID, app.ID, row.ID)
	if err != nil || got.Investigation.Notes != "Edited during check" || got.Assessment.InvestigationRevision == got.Revision {
		t.Fatal("old result appears current", got, err)
	}
}

func TestCPUProfileRegressionUnavailableAndDashboardCSRF(t *testing.T) {
	e, app, req, backend := profileInvestigationFixture(t)
	req.Investigation.Baseline.Start = req.Investigation.Baseline.Start.Add(-30 * 24 * time.Hour)
	req.Investigation.Baseline.End = req.Investigation.Baseline.End.Add(-30 * 24 * time.Hour)
	row, err := e.store.SaveProfileInvestigation(t.Context(), e.acct.ID, app.ID, "", req)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e.s.sessions = mgr
	token, err := middleware.IssueForAuthenticatedNamed(mgr, profileInvestigationCSRFAction, e.acct.ID, profileInvestigationCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	v := url.Values{"csrf_token": {token}, "expected_revision": {"1"}, "relative_increase_percent": {"20"}, "absolute_increase_cpu_per_second": {"0.01"}, "minimum_profiles": {"3"}, "minimum_coverage_ratio": {"0.8"}}
	post := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/dashboard/apps/"+app.Slug+"/profiles/investigations/"+row.ID+"/check", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("slug", app.Slug)
		r.SetPathValue("id", row.ID)
		r = r.WithContext(context.WithValue(r.Context(), accountContextKey{}, e.acct))
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: profileInvestigationCSRFCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		e.s.dashboardCheckProfileRegression(rec, r)
		return rec
	}
	if rec := post(""); rec.Code != 400 {
		t.Fatal("missing CSRF accepted", rec.Code)
	}
	if rec := post(token); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "investigation_id="+row.ID) {
		t.Fatal(rec.Code, rec.Body.String())
	}
	got, err := e.store.GetProfileInvestigation(t.Context(), e.acct.ID, app.ID, row.ID)
	if err != nil || got.Revision != 2 || got.Assessment == nil || got.Assessment.Status != "inconclusive" || got.Assessment.Total != nil || backend.queries != 0 || !got.Assessment.Baseline.Start.Equal(req.Investigation.Baseline.Start) {
		t.Fatal("unavailable history silently substituted", got, err)
	}
}
