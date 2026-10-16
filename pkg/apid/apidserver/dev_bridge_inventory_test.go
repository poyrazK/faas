package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 379
func TestDevBridgeInventoryDashboardAndScopedRevocation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	(*(*e.S()).DevBridgeEnabled()) = true
	project, err := (*e.Store()).CreateProject(t.Context(), state.Project{AccountID: (*e.Acct()).ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (*e.Store()).CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "development"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, ProjectID: project.ID, Slug: "payments", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	created := e.Do(t, "POST", "/v1/dev/bridges", api.CreateDevBridgeRequest{App: app.Slug, Environment: "development", DeveloperID: "alice"}, nil)
	var session api.CreateDevBridgeResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &session) != nil {
		t.Fatalf("create: %s", created.Body)
	}
	inventory := e.Do(t, "GET", "/v1/dev/bridges", nil, nil)
	if inventory.Code != 200 || !strings.Contains(inventory.Body.String(), session.Session.ID) || strings.Contains(inventory.Body.String(), session.Credentials.AttachmentToken) {
		t.Fatalf("inventory: %s", inventory.Body)
	}
	request := httptest.NewRequest("GET", "/dashboard/dev-bridges/"+session.Session.ID, nil)
	request.SetPathValue("id", session.Session.ID)
	request = request.WithContext(apidserver.WithAccount(request.Context(), (*e.Acct())))
	page := httptest.NewRecorder()
	(*e.S()).RenderDevBridgesDashboard(page, request)
	for _, want := range []string{"payments", "shop", "development", "alice", "unknown", "Revoke session"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(page.Body.String(), session.Credentials.RequestToken) {
		t.Fatal("credential leaked in dashboard")
	}
	other, err := (*e.Store()).CreateAccount(t.Context(), "bridge-inventory-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := (*e.Store()).ListDevBridges(t.Context(), other.ID, api.DevBridgeInventoryLimit); err != nil || len(rows) != 0 {
		t.Fatalf("cross-account inventory: %v %v", rows, err)
	}
	for _, owner := range []state.Account{(*e.Acct()), other} {
		cross := httptest.NewRequest("GET", "/activity", nil)
		cross.SetPathValue("id", session.Session.ID)
		response := httptest.NewRecorder()
		(*e.S()).GetDevBridgeActivity(response, cross, owner)
		want := 200
		if owner.ID != (*e.Acct()).ID {
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
		r.AddCookie(&http.Cookie{Name: apidserver.DashboardDevBridgeCSRFCookie, Value: token})
		r.SetPathValue("id", session.Session.ID)
		r = r.WithContext(apidserver.WithAccount(context.Background(), owner))
		response := httptest.NewRecorder()
		(*e.S()).RevokeDevBridgeDashboard(response, r)
		return response
	}
	if got := post((*e.Acct()), ""); got.Code != 403 {
		t.Fatalf("missing csrf: %d", got.Code)
	}
	otherToken, _ := middleware.IssueForAuthenticatedNamed((*(*e.S()).Sessions()), apidserver.DashboardDevBridgeAction, other.ID, apidserver.DashboardDevBridgeCSRFCookie)
	if got := post(other, otherToken); got.Code != 404 {
		t.Fatalf("cross-account revoke: %d", got.Code)
	}
	token, _ := middleware.IssueForAuthenticatedNamed((*(*e.S()).Sessions()), apidserver.DashboardDevBridgeAction, (*e.Acct()).ID, apidserver.DashboardDevBridgeCSRFCookie)
	if got := post((*e.Acct()), token); got.Code != 303 {
		t.Fatalf("revoke: %d %s", got.Code, got.Body)
	}
	if rows, err := (*e.Store()).ListDevBridges(t.Context(), (*e.Acct()).ID, api.DevBridgeInventoryLimit); err != nil || len(rows) != 0 {
		t.Fatalf("revoked lease retained in active inventory: %v %v", rows, err)
	}
}

// adr: 379 — the actual dashboard routes require completed MFA and a session.
func TestDevBridgeDashboardRoutesRequireCompletedAuthentication(t *testing.T) {
	e := apidserver.SetupWithMFA(t, api.PlanPro, false, false)
	e.GenerateEnrolledAccount(t)
	pending := e.MfaIssueWithPending(t, true)
	for _, route := range []struct{ method, path string }{{"GET", "/dashboard/dev-bridges"}, {"GET", "/dashboard/dev-bridges/session"}, {"POST", "/dashboard/dev-bridges/session/revoke"}} {
		for _, cookie := range []*http.Cookie{nil, pending} {
			r := httptest.NewRequest(route.method, route.path, nil)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			(*e.H()).ServeHTTP(rec, r)
			want := apidserver.LoginPath
			if cookie != nil {
				want = apidserver.DashboardMFAPath
			}
			if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), want) {
				t.Fatalf("%s %s: %d %q", route.method, route.path, rec.Code, rec.Header().Get("Location"))
			}
		}
	}
}
