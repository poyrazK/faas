package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func secretReferenceDashboardPost(t *testing.T, h http.Handler, session *http.Cookie, get *httptest.ResponseRecorder, path string, form url.Values, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	section := strings.SplitN(get.Body.String(), "<h2>Environment secret references</h2>", 2)
	if len(section) != 2 {
		t.Fatal("reference editor missing")
	}
	form.Set("csrf_token", dashboardInputToken(section[1]))
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	if csrf {
		cookie := findDashboardCookie(get.Result().Cookies(), dashboardSecretCSRFCookie)
		if cookie == nil {
			t.Fatal("missing secret CSRF cookie")
		}
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestSecretReferenceDashboardShortCatalogNamesAreWritable(t *testing.T) {
	h, session, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	project, app := seedPublicSecretReferences(t, store, acct)
	for _, scope := range []string{"qa", "1"} {
		if _, err := store.CreateProjectEnvironment(t.Context(), state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: scope}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAppSecretInScope(t.Context(), acct.ID, app.ID, scope, "DATABASE", []byte("sealed-"+scope)); err != nil {
			t.Fatal(err)
		}
	}
	page := cookieDo(t, h, session, http.MethodGet, "/dashboard/apps/shop-api/env?scope=__all__", nil)
	if page.Code != 200 {
		t.Fatalf("page: %d", page.Code)
	}
	body := page.Body.String()
	if !strings.Contains(body, "Save reference") || strings.Contains(body, "Unavailable environments:") {
		t.Fatal("accepted catalog scopes appeared unavailable")
	}
	for _, scope := range []string{"qa", "1"} {
		if !strings.Contains(body, `option value="`+scope+`"`) {
			t.Fatalf("catalog scope %s is missing from the editor", scope)
		}
		form := url.Values{"environment": {scope}, "key": {"DATABASE_URL"}, "reference": {"secret:DATABASE"}}
		if rec := secretReferenceDashboardPost(t, h, session, page, "/dashboard/apps/shop-api/secret-references", form, true); rec.Code != 302 {
			t.Fatalf("save %s: %d %s", scope, rec.Code, rec.Body.String())
		}
		refs, err := store.AppEnvironmentSecretReferences(t.Context(), acct.ID, app.ID, scope)
		if err != nil || refs["DATABASE_URL"] != "secret:DATABASE" {
			t.Fatalf("saved catalog reference: %+v %v", refs, err)
		}
	}
}

func TestSecretReferenceDashboardSessionFormsAndAuthority(t *testing.T) {
	h, session, store, mgr := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	project, app := seedPublicSecretReferences(t, store, acct)
	get := cookieDo(t, h, session, http.MethodGet, "/dashboard/apps/shop-api/env?scope=production", nil)
	if get.Code != 200 || strings.Contains(get.Body.String(), "sealed-production") {
		t.Fatalf("page: %d", get.Code)
	}
	path := "/dashboard/apps/shop-api/secret-references"
	form := url.Values{"environment": {"production"}, "key": {"DATABASE_URL"}, "reference": {"secret:DATABASE"}}
	if rec := secretReferenceDashboardPost(t, h, session, get, path, form, false); rec.Code != 400 {
		t.Fatalf("missing CSRF: %d %s", rec.Code, rec.Body.String())
	}
	if rec := secretReferenceDashboardPost(t, h, session, get, path, form, true); rec.Code != 302 || !strings.Contains(rec.Header().Get("Location"), "scope=production") {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	refs, err := store.AppEnvironmentSecretReferences(t.Context(), acct.ID, app.ID, "production")
	if err != nil || refs["DATABASE_URL"] != "secret:DATABASE" {
		t.Fatalf("saved reference: %+v %v", refs, err)
	}
	page := cookieDo(t, h, session, http.MethodGet, "/dashboard/apps/shop-api/env?scope=production", nil)
	if !strings.Contains(page.Body.String(), "secret:DATABASE") || !strings.Contains(page.Body.String(), "Removing a reference preserves its secret") {
		t.Fatal("names or deletion semantics missing")
	}
	if rec := secretReferenceDashboardPost(t, h, session, page, path+"/DATABASE_URL/delete", form, true); rec.Code != 302 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := store.GetAppSecretInScope(t.Context(), acct.ID, app.ID, "production", "DATABASE"); err != nil {
		t.Fatal("reference deletion removed sealed source")
	}
	// An unverified session cannot mutate even with a valid account-bound CSRF pair.
	sid := uuid.NewString()
	if _, err := store.CreateSession(t.Context(), sid, acct.ID, "192.0.2.30", "reference-test"); err != nil {
		t.Fatal(err)
	}
	pending := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)
	if rec := secretReferenceDashboardPost(t, h, pending, page, path, form, true); rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), dashboardMFAPath) {
		t.Fatalf("pending MFA: %d %s", rec.Code, rec.Body.String())
	}
	_ = ownPublicSecretReference(t, store, acct, project, app)
	if rec := secretReferenceDashboardPost(t, h, session, page, path, form, true); rec.Code != 409 || !strings.Contains(rec.Body.String(), "environment_field_git_managed") {
		t.Fatalf("owned save: %d %s", rec.Code, rec.Body.String())
	}
}
