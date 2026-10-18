package apidserver_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlGuidedSetup(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "guided-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "guided-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "guided-auth"})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice internal", 100)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner, keyHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, keyHash, "setup-owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	srv.ProductControl().AuthOrigin = func(_ context.Context, account state.Account, slug string) (state.App, string, error) {
		if account.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrProductAccess
		}
		return auth, "https://guided-auth.example.com", nil
	}
	handler := srv.Handler()
	base := "/dashboard/apps/" + app.Slug + "/product-control"
	doctor := "/v1/apps/" + app.Slug + "/product/doctor"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	call := func(method, path string, form url.Values, bearer bool, status int) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if form != nil {
			body = form.Encode()
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if bearer {
			r.Header.Set("Authorization", "Bearer "+owner)
		} else {
			for _, cookie := range jar {
				r.AddCookie(cookie)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		response := w.Result()
		defer func() { _ = response.Body.Close() }()
		for _, cookie := range response.Cookies() {
			jar[cookie.Name] = cookie
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("setup/doctor response cacheable")
		}
		return w
	}
	for _, route := range []struct{ method, path string }{{"GET", base + "/setup"}, {"GET", base + "/doctor"}, {"POST", base + "/setup/review"}, {"POST", base + "/setup/confirm"}, {"GET", doctor}} {
		call(route.method, route.path, nil, false, 404)
	}
	srv.WithProductControlEnabled(true)
	page := call("GET", base, nil, false, 200)
	if !strings.Contains(page.Body.String(), "Start with guided setup") {
		t.Fatal("new products cannot reach setup")
	}
	page = call("GET", base+"/setup", nil, false, 200)
	form := ownerConsoleForm(t, page.Body.String(), "")
	form.Set("auth_app", auth.Slug)
	if !strings.Contains(page.Body.String(), tenant.Name) {
		t.Fatal("existing customer names missing")
	}
	noCSRF := ownerConsoleCopy(form)
	noCSRF.Del("csrf_token")
	call("POST", base+"/setup/review", noCSRF, false, 403)
	invalid := ownerConsoleCopy(form)
	invalid.Set("auth_app", "foreign-auth")
	call("POST", base+"/setup/review", invalid, false, 400)
	preview := call("POST", base+"/setup/review", form, false, 200)
	if _, err := store.GetProductCatalog(ctx, acct.ID, app.ID, ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("preview published configuration")
	}
	confirm := ownerConsoleForm(t, preview.Body.String(), "confirm")
	raw, err := base64.RawURLEncoding.DecodeString(confirm.Get("payload"))
	if err != nil {
		t.Fatal(err)
	}
	tamper := ownerConsoleCopy(confirm)
	tamper.Set("payload", base64.RawURLEncoding.EncodeToString([]byte(strings.Replace(string(raw), auth.Slug, "foreign-auth", 1))))
	call("POST", base+"/setup/confirm", tamper, false, 403)
	call("POST", base+"/setup/confirm", confirm, false, 200)
	call("POST", base+"/setup/confirm", confirm, false, 200)
	first, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || len(first.Spec.Plans) != 2 || first.Spec.AuthApp != auth.Slug {
		t.Fatalf("first publication: %+v %v", first, err)
	}
	assigned, err := store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	page = call("GET", base+"/setup", nil, false, 200)
	form = ownerConsoleForm(t, page.Body.String(), "")
	form.Set("starter_price", "30000")
	preview = call("POST", base+"/setup/review", form, false, 200)
	stale := ownerConsoleForm(t, preview.Body.String(), "confirm")
	changed := first.Spec
	changed.Features["other"] = api.ProductFeature{Label: "Other feature", Operation: &api.ProductOperationBinding{Name: "other", Scope: api.DefaultEnvScope}}
	later, err := store.PutProductCatalog(ctx, acct.ID, app.ID, changed)
	if err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/setup/confirm", stale, false, 409)
	head, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || head.Revision != later.Revision {
		t.Fatal("stale confirmation replaced newer catalog")
	}
	page = call("GET", base+"/setup", nil, false, 200)
	form = ownerConsoleForm(t, page.Body.String(), "")
	form.Set("starter_price", "30000")
	preview = call("POST", base+"/setup/review", form, false, 200)
	for _, text := range []string{"EUR 0.25000", "EUR 0.30000", "Other feature"} {
		if !strings.Contains(preview.Body.String(), text) {
			t.Fatalf("preview missing %s", text)
		}
	}
	call("POST", base+"/setup/confirm", ownerConsoleForm(t, preview.Body.String(), "confirm"), false, 200)
	current, err := store.GetProductCustomer(ctx, acct.ID, app.ID, tenant.ID)
	if err != nil || current != assigned {
		t.Fatal("setup changed customer price revision or identity")
	}
	var report api.ProductDoctorReport
	w := call("GET", doctor+"?tenant_id="+tenant.ID, nil, true, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Revision != first.Revision || report.HeadRevision == first.Revision || report.TenantID != tenant.ID || report.ConfigurationState != "blocked" || report.QualificationState != "unverified" {
		t.Fatalf("readiness scope/claims: %+v", report)
	}
	call("GET", base+"/doctor?tenant="+tenant.ID, nil, false, 200)
	call("GET", doctor+"?tenant_id="+uuid.NewString(), nil, true, 404)
	call("GET", doctor+"?tenant_id="+tenant.ID+"&tenant_id="+tenant.ID, nil, true, 400)
	srv.WithProductControlEnabled(false)
	call("GET", doctor, nil, true, 404)
}
