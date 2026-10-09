package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func profileInvestigationFixture(t *testing.T) (testEnv, state.App, api.SaveProfileInvestigationRequest, *profileQueryCapture) {
	t.Helper()
	e := setup(t, api.PlanHobby)
	backend := &profileQueryCapture{}
	e.s.profileBackend = backend
	e.s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, Slug: "saved-profiles", Runtime: "node24", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Scope: "prod"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := api.ProfileQuery{DeploymentID: dep.ID, Runtime: "node24", Start: now.Add(-time.Hour), End: now}
	zero := int64(0)
	req := api.SaveProfileInvestigationRequest{ExpectedRevision: &zero, Investigation: api.ProfileInvestigationInput{Title: "CPU regression", Findings: "parseJSON", Notes: "Compare equal traffic", Baseline: q, Candidate: q, SelectedPath: &api.ProfileCallPath{View: "comparison", Frames: []api.ProfileCallPathFrame{{Name: "all"}, {Name: "parseJSON", File: "app.js", Line: 42}}}}}
	return e, app, req, backend
}

func TestCPUProfileSavedInvestigationAPIIsolationAndRevision(t *testing.T) {
	e, app, req, backend := profileInvestigationFixture(t)
	path := "/v1/apps/" + app.Slug + "/profiles/investigations"
	created := e.do(t, "POST", path, req, nil)
	var out api.ProfileInvestigationResponse
	if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if created.Code != 201 || out.Saved.Revision != 1 || out.BaselineStatus.Status != "retained" || !strings.Contains(out.URL, "investigation_id="+out.Saved.ID) {
		t.Fatal(created.Code, created.Body.String())
	}
	if backend.queries != 0 {
		t.Fatal("metadata save queried CPU backend")
	}
	get := e.do(t, "GET", path+"/"+out.Saved.ID, nil, nil)
	if get.Code != 200 {
		t.Fatal(get.Code, get.Body.String())
	}
	list := e.do(t, "GET", path, nil, nil)
	if list.Code != 200 || !strings.Contains(list.Body.String(), out.Saved.ID) {
		t.Fatal(list.Code, list.Body.String())
	}
	readKey, hash, _ := api.GenerateAPIKey()
	if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "profiles-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	readHeaders := map[string]string{"Authorization": "Bearer " + readKey}
	if rec := e.do(t, "GET", path+"/"+out.Saved.ID, nil, readHeaders); rec.Code != 200 {
		t.Fatal("read key", rec.Code)
	}
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		target := path
		if method != "POST" {
			target += "/" + out.Saved.ID + "?expected_revision=1"
		}
		if rec := e.do(t, method, target, req, readHeaders); rec.Code != 403 {
			t.Fatal("read-only mutation", method, rec.Code)
		}
	}
	other, err := e.store.CreateApp(t.Context(), state.App{AccountID: "foreign-account", Slug: "foreign-profiles", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	bad := req
	bad.Investigation.Candidate.DeploymentID = foreign.ID
	if rec := e.do(t, "POST", path, bad, nil); rec.Code != 404 {
		t.Fatal("foreign selection saved", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", "/v1/apps/"+other.Slug+"/profiles/investigations/"+out.Saved.ID, nil, nil); rec.Code != 404 {
		t.Fatal("foreign app exposed metadata", rec.Code)
	}
	if rec := e.do(t, "PUT", path+"/"+out.Saved.ID, req, nil); rec.Code != 409 {
		t.Fatal("stale update", rec.Code)
	}
	req.ExpectedRevision = &out.Saved.Revision
	req.Investigation.Notes = "Updated finding"
	updated := e.do(t, "PUT", path+"/"+out.Saved.ID, req, nil)
	if updated.Code != 200 {
		t.Fatal(updated.Code, updated.Body.String())
	}
	if rec := e.do(t, "DELETE", path+"/"+out.Saved.ID+"?expected_revision=1", nil, nil); rec.Code != 409 {
		t.Fatal("stale delete", rec.Code)
	}
	if rec := e.do(t, "DELETE", path+"/"+out.Saved.ID+"?expected_revision=2", nil, nil); rec.Code != 204 {
		t.Fatal("delete", rec.Code, rec.Body.String())
	}
	if rec := e.do(t, "GET", path+"/"+out.Saved.ID, nil, nil); rec.Code != 404 {
		t.Fatal("deleted read", rec.Code)
	}
	if rec := e.do(t, "GET", path, nil, map[string]string{"Authorization": ""}); rec.Code != 401 {
		t.Fatal("unauthenticated metadata", rec.Code)
	}
}

func TestCPUProfileSavedInvestigationExpiryAndPlanDowngrade(t *testing.T) {
	e, app, req, backend := profileInvestigationFixture(t)
	req.Investigation.Baseline.Start = req.Investigation.Baseline.Start.Add(-30 * 24 * time.Hour)
	req.Investigation.Baseline.End = req.Investigation.Baseline.End.Add(-30 * 24 * time.Hour)
	req.Investigation.Candidate = req.Investigation.Baseline
	path := "/v1/apps/" + app.Slug + "/profiles/investigations"
	if rec := e.do(t, "POST", path, req, nil); rec.Code != 400 {
		t.Fatal("new expired window accepted", rec.Code)
	}
	row, err := e.store.SaveProfileInvestigation(t.Context(), e.acct.ID, app.ID, "", req)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []api.Plan{api.PlanHobby, api.PlanFree} {
		acct := e.acct
		acct.Plan = plan
		out := e.s.profileInvestigationResponse(t.Context(), acct, app, row)
		want := "expired"
		if plan == api.PlanFree {
			want = "plan_unavailable"
		}
		if out.BaselineStatus.Status != want || out.CandidateStatus.Status != want {
			t.Fatal("expiry/plan status", out)
		}
		reopened := httptest.NewRequest(http.MethodGet, out.URL+"&deployment_id=foreign", nil)
		data, _, ok := e.s.savedProfilePage(httptest.NewRecorder(), reopened, acct, app)
		if !ok || data.Profile != nil || data.Compare != nil || !data.Query.Start.Equal(req.Investigation.Candidate.Start) || data.Investigations.Saved.Saved.Investigation.Notes != row.Investigation.Notes || backend.queries != 0 {
			t.Fatal("expired comparison silently changed or queried", data)
		}
		req.ExpectedRevision = &row.Revision
		req.Investigation.Notes = "Notes remain editable"
		var problem *api.Problem
		row, problem = e.s.saveOwnedProfileInvestigation(t.Context(), acct, app, e.store, row.ID, req)
		if problem != nil || row.Investigation.Notes != req.Investigation.Notes {
			t.Fatal("commentary locked after expiry", problem)
		}
	}
	got := e.do(t, "GET", path+"/"+row.ID, nil, nil)
	if got.Code != 200 || !strings.Contains(got.Body.String(), `"status":"expired"`) {
		t.Fatal("expired API metadata", got.Code, got.Body.String())
	}
}

func TestCPUProfileSavedInvestigationDashboardCSRFAndSelections(t *testing.T) {
	e, app, req, backend := profileInvestigationFixture(t)
	mgr, err := session.NewEphemeralManager(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	e.s.sessions = mgr
	token, err := middleware.IssueForAuthenticatedNamed(mgr, profileInvestigationCSRFAction, e.acct.ID, profileInvestigationCSRFCookie)
	if err != nil {
		t.Fatal(err)
	}
	q := req.Investigation.Candidate
	b := req.Investigation.Baseline
	v := url.Values{"csrf_token": {token}, "expected_revision": {"0"}, "title": {"Performance findings"}, "notes": {"Team notes"}, "deployment_id": {q.DeploymentID}, "runtime": {q.Runtime}, "start": {q.Start.Format(time.RFC3339Nano)}, "end": {q.End.Format(time.RFC3339Nano)}, "baseline_id": {b.DeploymentID}, "baseline_start": {b.Start.Format(time.RFC3339Nano)}, "baseline_end": {b.End.Format(time.RFC3339Nano)}}
	post := func(cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/dashboard/apps/"+app.Slug+"/profiles/investigations", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("slug", app.Slug)
		r = r.WithContext(context.WithValue(r.Context(), accountContextKey{}, e.acct))
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: profileInvestigationCSRFCookie, Value: cookie})
		}
		rec := httptest.NewRecorder()
		e.s.dashboardSaveProfileInvestigation(rec, r)
		return rec
	}
	if rec := post(""); rec.Code != 400 {
		t.Fatal("missing CSRF accepted", rec.Code)
	}
	rec := post(token)
	if rec.Code != 303 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	id := location.Query().Get("investigation_id")
	for range api.ProfileMaxDeploymentChoices + 1 {
		if _, err := e.store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: state.DeploymentKindTarball, Scope: "prod"}); err != nil {
			t.Fatal(err)
		}
	}
	backend.queries = 0
	r := httptest.NewRequest(http.MethodGet, location.String()+"&deployment_id="+uuid.NewString(), nil)
	data, _, ok := e.s.savedProfilePage(httptest.NewRecorder(), r, e.acct, app)
	if !ok || data.Query.DeploymentID != q.DeploymentID || data.Baseline.DeploymentID != b.DeploymentID || data.Investigations.Saved.Saved.ID != id || data.Investigations.CSRF == "" || backend.queries != 2 {
		t.Fatal("saved selection not restored", data, backend.queries)
	}
	found := false
	for _, dep := range data.Deployments {
		if dep.ID == q.DeploymentID {
			found = true
		}
	}
	if !found || len(data.Deployments) > api.ProfileMaxDeploymentChoices {
		t.Fatal("old saved deployment fell out of the bounded selector")
	}
	row, err := e.store.GetProfileInvestigation(t.Context(), e.acct.ID, app.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	v.Set("id", id)
	v.Set("expected_revision", strconv.FormatInt(row.Revision, 10))
	v.Set("action", "delete")
	if rec := post(token); rec.Code != 303 {
		t.Fatal("dashboard delete", rec.Code, rec.Body.String())
	}
}
