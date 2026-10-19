package apidserver

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPMaintenanceFormsAndGuards(t *testing.T) {
	t.Cleanup(withTestRecipient(t))
	handler, cookie, store, manager := newAuthedDashboardServerFull(t)
	owner, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), owner.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "maintenance-form", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(t.Context(), "maintenance-form-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "maintenance-form-other", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/dashboard/mcp?app="+app.Slug+"&scope=staging", nil)
	r.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, r)
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == dashboardMCPAlertCookie {
			csrf = c
		}
	}
	if page.Code != 200 || csrf == nil || !strings.Contains(page.Body.String(), "Schedule maintenance") {
		t.Fatal("missing maintenance form", page.Code)
	}
	form := url.Values{"csrf_token": {csrf.Value}, "scope": {"staging"}, "ends_at": {time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, "reason": {"rolling upgrade"}}
	post := func(path string, withCSRF bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(cookie)
		if withCSRF {
			r.AddCookie(csrf)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	path := "/dashboard/mcp/apps/" + app.Slug + "/maintenance"
	if w := post(path, false); w.Code != 400 {
		t.Fatal("CSRF bypass", w.Code)
	}
	if w := post("/dashboard/mcp/apps/maintenance-form-other/maintenance", true); w.Code != 404 {
		t.Fatal("ownership bypass", w.Code, w.Body.String())
	}
	if w := post(path, true); w.Code != 303 {
		t.Fatal("create", w.Code, w.Body.String())
	}
	pageOfWindows, err := store.ListMCPReadinessMaintenance(t.Context(), owner.ID, app.ID, "staging", "", 20)
	if err != nil || len(pageOfWindows.Windows) != 1 {
		t.Fatal("not persisted", pageOfWindows, err)
	}
	form.Del("ends_at")
	form.Set("reason", "upgrade complete")
	if w := post(path+"/"+pageOfWindows.Windows[0].ID+"/cancel", true); w.Code != 303 {
		t.Fatal("cancel", w.Code, w.Body.String())
	}

	env, err := manager.Verify(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := manager.IssueWithSessionAndBindingHash(env.Sid, owner.ID, env.BindingHash, true)
	if err != nil {
		t.Fatal(err)
	}
	clearedCookie := cookie
	cookie = &http.Cookie{Name: sessionCookie, Value: pending}
	form.Set("ends_at", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	if w := post(path, true); w.Code != http.StatusFound || w.Header().Get("Location") != dashboardMFAPath {
		t.Fatal("pending MFA bypass", w.Code, w.Body.String())
	}
	cookie = clearedCookie
	after, err := store.ListMCPReadinessMaintenance(t.Context(), owner.ID, app.ID, "staging", "", 20)
	if err != nil || len(after.Windows) != 1 || after.Windows[0].CancelledAt == nil {
		t.Fatal("pending session changed maintenance", after, err)
	}
	form.Add("scope", "production")
	if w := post(path, true); w.Code != 400 {
		t.Fatal("duplicate fields accepted", w.Code)
	}
}
