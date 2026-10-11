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

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCPUProfileAutomaticPolicyAPIAndScopes(t *testing.T) {
	e, app, _, backend := profileInvestigationFixture(t)
	path := "/v1/apps/" + app.Slug + "/profiles/deployment-policy"
	rec := e.do(t, "GET", path, nil, nil)
	var policy api.ProfileDeploymentPolicy
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil || rec.Code != 200 || policy.Revision != 0 || policy.Config.Enabled || policy.UpdatedAt != nil || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("initial policy", policy, err, rec.Code)
	}
	zero := int64(0)
	policy.Config.Enabled = true
	req := api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: &zero, Config: policy.Config}
	readKey, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(t, "PUT", path, req, map[string]string{"Authorization": "Bearer " + readKey}); rec.Code != 403 {
		t.Fatal("read key changed policy", rec.Code)
	}
	if rec := e.do(t, "PUT", path, req, map[string]string{"Authorization": ""}); rec.Code != 401 {
		t.Fatal("unauthenticated policy", rec.Code)
	}
	partial := map[string]any{"expected_revision": 0, "config": map[string]any{"runtime": "node24", "window_seconds": 300, "options": policy.Config.Options}}
	if rec := e.do(t, "PUT", path, partial, nil); rec.Code != 400 {
		t.Fatal("missing enabled/warmup silently defaulted", rec.Code)
	}
	rec = e.do(t, "PUT", path, req, nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &policy); err != nil || rec.Code != 200 || policy.Revision != 1 || !policy.Config.Enabled || policy.UpdatedAt == nil {
		t.Fatal("saved policy", policy, err, rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "PUT", path, req, nil); rec.Code != 409 || !strings.Contains(rec.Body.String(), "Policy changed") {
		t.Fatal("stale policy overwritten", rec.Code)
	}
	for _, suffix := range []string{"deployment-policy", "deployment-checks"} {
		if rec := e.do(t, "GET", "/v1/apps/"+app.Slug+"/profiles/"+suffix, nil, map[string]string{"Authorization": "Bearer " + readKey}); rec.Code != 200 {
			t.Fatal("read scope denied", rec.Code)
		}
	}
	if rec := e.do(t, "GET", "/v1/apps/"+app.Slug+"/profiles/deployment-checks/"+uuid.NewString(), nil, nil); rec.Code != 404 {
		t.Fatal("foreign receipt", rec.Code)
	}
	if backend.queries != 0 {
		t.Fatal("metadata operation queried raw profiles")
	}
	e.s.profileBackend = nil
	req.ExpectedRevision = &policy.Revision
	if rec := e.do(t, "PUT", path, req, nil); rec.Code != 503 {
		t.Fatal("unavailable backend enabled", rec.Code)
	}
	req.Config.Enabled = false
	if rec := e.do(t, "PUT", path, req, nil); rec.Code != 200 {
		t.Fatal("cannot disable unavailable backend", rec.Code)
	}
	if err := e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	policy, err := e.store.GetProfileDeploymentPolicy(t.Context(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedRevision, req.Config.Enabled = &policy.Revision, true
	if rec := e.do(t, "PUT", path, req, nil); rec.Code != 402 {
		t.Fatal("free plan enabled", rec.Code)
	}
	if rec := e.do(t, "GET", path, nil, nil); rec.Code != 200 {
		t.Fatal("downgrade hid metadata", rec.Code)
	}
}

func TestCPUProfileAutomaticWorkerIsolationAndCoverage(t *testing.T) {
	e, app, req, _ := profileInvestigationFixture(t)
	req.Investigation.Candidate.End = time.Now().Add(-time.Minute)
	req.Investigation.Candidate.Start = req.Investigation.Candidate.End.Add(-5 * time.Minute)
	req.Investigation.Baseline = req.Investigation.Candidate
	req.Investigation.Baseline.Start = req.Investigation.Baseline.Start.Add(-10 * time.Minute)
	req.Investigation.Baseline.End = req.Investigation.Baseline.End.Add(-10 * time.Minute)
	backend := &regressionQueryCapture{baseline: req.Investigation.Baseline}
	e.s.profileBackend = backend
	work := state.ProfileDeploymentCheckWork{AccountID: e.acct.ID, Check: api.ProfileDeploymentCheck{AppID: app.ID, Scope: "prod", Baseline: &req.Investigation.Baseline, Candidate: req.Investigation.Candidate, Attempts: 1, Config: state.DefaultProfileDeploymentPolicy(app.ID, "node24").Config}}
	assessment, retry := e.s.assessProfileDeploymentCheck(t.Context(), work)
	if assessment.Status != "regressed" || retry || backend.queries != 2 || backend.tenant != e.acct.ID || backend.app != app.ID || backend.scope != "prod" || assessment.Candidate.Runtime != "node24" {
		t.Fatal("worker identity/assessment", assessment, retry, backend)
	}
	work.Check.Scope = "staging"
	if a, retry := e.s.assessProfileDeploymentCheck(t.Context(), work); a.Status != "inconclusive" || retry || backend.queries != 2 {
		t.Fatal("cross-environment query", a, retry)
	}
	work.Check.Scope = "prod"
	work.Check.Baseline = nil
	if a, retry := e.s.assessProfileDeploymentCheck(t.Context(), work); a.Status != "inconclusive" || retry || backend.queries != 2 {
		t.Fatal("missing predecessor queried", a, retry)
	}
	work.Check.Baseline = &req.Investigation.Baseline
	missing := &profileQueryCapture{}
	e.s.profileBackend = missing
	if a, retry := e.s.assessProfileDeploymentCheck(t.Context(), work); a.Status != "inconclusive" || !retry {
		t.Fatal("missing coverage marked safe", a, retry)
	}
	work.Check.Attempts = api.ProfileAutoMaxAttempts
	if a, retry := e.s.assessProfileDeploymentCheck(t.Context(), work); a.Status != "inconclusive" || retry {
		t.Fatal("retry limit ignored", a, retry)
	}
	q := *work.Check.Baseline
	q.Start, q.End = q.Start.Add(-30*24*time.Hour), q.End.Add(-30*24*time.Hour)
	work.Check.Baseline = &q
	queries := missing.queries
	if a, retry := e.s.assessProfileDeploymentCheck(t.Context(), work); a.Status != "inconclusive" || retry || missing.queries != queries {
		t.Fatal("expired window substituted", a, retry)
	}
	work.Check.Baseline = &req.Investigation.Baseline
	if err := e.store.UpdateAccountPlan(t.Context(), e.acct.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if a, retry := e.s.assessProfileDeploymentCheck(t.Context(), work); a.Status != "inconclusive" || retry || missing.queries != queries {
		t.Fatal("plan downgrade queried profiles", a, retry)
	}
}

func TestCPUProfileAutomaticDashboardPolicyCSRF(t *testing.T) {
	e, app, _, backend := profileInvestigationFixture(t)
	mgr, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e.s.sessions = mgr
	token, err := middleware.IssueForAuthenticatedNamed(mgr, profileDeploymentPolicyAction, e.acct.ID, profileDeploymentPolicyCookie)
	if err != nil {
		t.Fatal(err)
	}
	v := url.Values{"csrf_token": {token}, "expected_revision": {"0"}, "enabled": {"on"}, "runtime": {"node24"}, "window_seconds": {"300"}, "warmup_seconds": {"120"}, "relative_increase_percent": {"20"}, "absolute_increase_cpu_per_second": {"0.01"}, "minimum_profiles": {"3"}, "minimum_coverage_ratio": {"0.8"}}
	post := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/dashboard/apps/"+app.Slug+"/profiles/deployment-policy", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("slug", app.Slug)
		r = r.WithContext(context.WithValue(r.Context(), accountContextKey{}, e.acct))
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: profileDeploymentPolicyCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		e.s.dashboardSaveProfileDeploymentPolicy(rec, r)
		return rec
	}
	if rec := post(""); rec.Code != 400 {
		t.Fatal("missing CSRF accepted", rec.Code)
	}
	if rec := post(token); rec.Code != 303 || !strings.HasSuffix(rec.Header().Get("Location"), "#automatic-profile-checks") {
		t.Fatal(rec.Code, rec.Body.String())
	}
	p, err := e.store.GetProfileDeploymentPolicy(t.Context(), e.acct.ID, app.ID)
	if err != nil || p.Revision != 1 || !p.Config.Enabled || backend.queries != 0 {
		t.Fatal(p, err)
	}
	if rec := post(token); rec.Code != 409 {
		t.Fatal("dashboard stale policy overwritten", rec.Code)
	}
}
