package apidserver_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlCustomerActivityHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := t.Context(), state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "activity-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "activity-http"})
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "activity-owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "activity-foreign@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey, foreignHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, foreign.ID, foreignHash, "other-owner", api.ScopesAdminOnly); err != nil {
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
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	handler := srv.Handler()
	tenantID := uuid.NewString()
	apiBase := "/v1/apps/" + app.Slug + "/product/customers/activity"
	apiPath := apiBase + "?tenant=" + tenantID
	pagePath := "/dashboard/apps/" + app.Slug + "/product-control/activity?tenant=" + tenantID
	call := func(method, path, bearer string, cookie bool, body string, status int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://api.example"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sid})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private activity was cacheable")
		}
		return w
	}
	call("GET", apiPath, "", false, "", 404)
	call("POST", apiPath, "", false, "", 404)
	call("DELETE", apiPath, "", false, "", 404)
	call("GET", pagePath, "", false, "", 404)
	call("POST", pagePath, "", false, "", 404)
	srv.WithProductControlEnabled(true)
	call("POST", apiPath, "", false, "", 405)
	call("GET", apiPath, "", false, "", 401)
	call("GET", apiPath, foreignKey, false, "", 404)
	call("GET", pagePath, key, false, "", 302)
	call("GET", apiPath, key, false, "", 404)
	_, err = store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "alice", "Alice <script>private</script>", 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter"}); err != nil {
		t.Fatal(err)
	}

	apiPath = apiBase + "?tenant=" + tenant.ID
	pagePath = "/dashboard/apps/" + app.Slug + "/product-control/activity?tenant=" + tenant.ID
	w := call("GET", apiPath, key, false, "", 200)
	var page api.ProductCustomerActivityPage
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) != 0 || page.Events == nil || page.TenantID != tenant.ID {
		t.Fatalf("legacy activity invented: %s %v", w.Body.String(), err)
	}
	preview, err := store.PreviewProductCustomerChange(ctx, acct.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", Suspended: true})
	if err != nil {
		t.Fatal(err)
	}
	change, err := store.ApplyProductCustomerChange(ctx, acct.ID, app.ID, api.ProductCustomerChangeRequest{ID: uuid.NewString(), ExpectedHash: preview.Hash, Customer: preview.After})
	if err != nil {
		t.Fatal(err)
	}
	w = call("GET", apiPath, key, false, "", 200)
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Events) != 1 || page.Events[0].SourceID != change.ID || page.Events[0].Assignment.Before.Suspended || !page.Events[0].Assignment.After.Suspended {
		t.Fatalf("retained assignment: %s %v", w.Body.String(), err)
	}
	w = call("GET", pagePath, "", true, "", 200)
	for _, want := range []string{"Customer activity timeline", "Owner assignment recorded", "new starts suspended", "Retained record details", change.ID, "historical evidence", "Auth team changes"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("activity page missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "<form") || strings.Contains(w.Body.String(), "preview_hash") {
		t.Fatal("read-only history exposed a mutation or signed review")
	}
	call("GET", apiBase, key, false, "", 400)
	for _, suffix := range []string{"&cursor=bad", "&cursor=a&cursor=b", "&tenant=" + tenant.ID, "&period=2026-09"} {
		call("GET", apiPath+suffix, key, false, "", 400)
	}
	call("GET", apiBase+"?tenant="+uuid.NewString(), key, false, "", 404)
	raw, err := json.Marshal(api.CreatePlatformTenantAccessTokenRequest{Name: "activity-customer", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
	if err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", key, false, string(raw), 201)
	var token api.CreatePlatformTenantAccessTokenResponse
	if err = json.Unmarshal(w.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	call("GET", apiPath, token.Token, false, "", 403)
	srv.WithProductControlEnabled(false)
	call("GET", apiPath, key, false, "", 404)
	call("GET", pagePath, "", true, "", 404)
}
