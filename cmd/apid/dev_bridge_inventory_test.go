package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 379
func TestDevBridgeInventoryDashboardAndScopedRevocation(t *testing.T) {
	e := setup(t, api.PlanPro)
	e.s.devBridgeEnabled = true
	project, err := e.store.CreateProject(t.Context(), state.Project{AccountID: e.acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(t.Context(), state.App{AccountID: e.acct.ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	created := e.do(t, "POST", "/v1/dev/bridges", api.CreateDevBridgeRequest{App: app.Slug, Environment: "development", DeveloperID: "alice"}, nil)
	var session api.CreateDevBridgeResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &session) != nil {
		t.Fatalf("create: %s", created.Body)
	}
	inventory := e.do(t, "GET", "/v1/dev/bridges", nil, nil)
	if inventory.Code != 200 || !strings.Contains(inventory.Body.String(), session.Session.ID) || strings.Contains(inventory.Body.String(), session.Credentials.AttachmentToken) {
		t.Fatalf("inventory: %s", inventory.Body)
	}
	request := httptest.NewRequest("GET", "/dashboard/dev-bridges/"+session.Session.ID, nil)
	request.SetPathValue("id", session.Session.ID)
	request = request.WithContext(WithAccount(request.Context(), e.acct))
	page := httptest.NewRecorder()
	e.s.renderDevBridgesDashboard(page, request)
	for _, want := range []string{"payments", "shop", "development", "alice", "unknown", "Revoke session"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), session.Credentials.RequestToken) {
		t.Fatal("credential leaked in dashboard")
	}
	other, err := e.store.CreateAccount(t.Context(), "bridge-inventory-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := e.store.ListDevBridges(t.Context(), other.ID, api.DevBridgeInventoryLimit); err != nil || len(rows) != 0 {
		t.Fatalf("cross-account inventory: %v %v", rows, err)
	}
	for _, owner := range []state.Account{e.acct, other} {
		cross := httptest.NewRequest("GET", "/activity", nil)
		cross.SetPathValue("id", session.Session.ID)
		response := httptest.NewRecorder()
		e.s.getDevBridgeActivity(response, cross, owner)
		want := 200
		if owner.ID != e.acct.ID {
			want = 404
		}
		if response.Code != want {
			t.Fatalf("activity ownership: %d want %d", response.Code, want)
		}
	}
	post := func(owner state.Account, token string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {token}}
		r := httptest.NewRequest("POST", "/revoke", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: dashboardDevBridgeCSRFCookie, Value: token})
		r.SetPathValue("id", session.Session.ID)
		r = r.WithContext(WithAccount(context.Background(), owner))
		response := httptest.NewRecorder()
		e.s.revokeDevBridgeDashboard(response, r)
		return response
	}
	if got := post(e.acct, ""); got.Code != 403 {
		t.Fatalf("missing csrf: %d", got.Code)
	}
	otherToken, _ := middleware.IssueForAuthenticatedNamed(e.s.sessions, dashboardDevBridgeAction, other.ID, dashboardDevBridgeCSRFCookie)
	if got := post(other, otherToken); got.Code != 404 {
		t.Fatalf("cross-account revoke: %d", got.Code)
	}
	token, _ := middleware.IssueForAuthenticatedNamed(e.s.sessions, dashboardDevBridgeAction, e.acct.ID, dashboardDevBridgeCSRFCookie)
	if got := post(e.acct, token); got.Code != 303 {
		t.Fatalf("revoke: %d %s", got.Code, got.Body)
	}
	if rows, err := e.store.ListDevBridges(t.Context(), e.acct.ID, api.DevBridgeInventoryLimit); err != nil || len(rows) != 0 {
		t.Fatalf("revoked lease retained in active inventory: %v %v", rows, err)
	}
}

// adr: 379 — the actual dashboard routes require completed MFA and a session.
func TestDevBridgeDashboardRoutesRequireCompletedAuthentication(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	e.generateEnrolledAccount(t)
	pending := e.mfaIssueWithPending(t, true)
	for _, route := range []struct{ method, path string }{{"GET", "/dashboard/dev-bridges"}, {"GET", "/dashboard/dev-bridges/session"}, {"POST", "/dashboard/dev-bridges/session/revoke"}} {
		for _, cookie := range []*http.Cookie{nil, pending} {
			r := httptest.NewRequest(route.method, route.path, nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			e.h.ServeHTTP(rec, r)
			want := loginPath
			if cookie != nil {
				want = dashboardMFAPath
			}
			if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), want) {
				t.Fatalf("%s %s: %d %q", route.method, route.path, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
}
