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

func TestProductControlCustomerOverviewHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx, store := t.Context(), state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "overview-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "overview-http"})
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "overview-owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "overview-foreign@example.com", api.PlanPro)
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
	apiPath := "/v1/apps/" + app.Slug + "/product/customers/overview"
	pagePath := "/dashboard/apps/" + app.Slug + "/product-control/customers"
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
			t.Fatal("private overview was cacheable")
		}
		return w
	}
	call("GET", apiPath, "", false, "", 404)
	call("GET", pagePath, "", false, "", 404)
	call("POST", pagePath, "", false, "", 404)
	srv.WithProductControlEnabled(true)
	call("GET", apiPath, "", false, "", 401)
	call("GET", apiPath, foreignKey, false, "", 404)
	call("GET", pagePath, key, false, "", 302)
	w := call("GET", apiPath, key, false, "", 200)
	var page api.ProductCustomerOverviewPage
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Customers) != 0 || page.Customers == nil {
		t.Fatalf("empty overview: %s %v", w.Body.String(), err)
	}
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
	w = call("GET", apiPath, key, false, "", 200)
	if err = json.Unmarshal(w.Body.Bytes(), &page); err != nil || len(page.Customers) != 1 || page.Customers[0].Readiness.ConfigurationState != "blocked" || page.Customers[0].Activation.State != "not_recorded" || page.QualificationState != "unverified" {
		t.Fatalf("overview claims: %s %v", w.Body.String(), err)
	}
	w = call("GET", pagePath, "", true, "", 200)
	for _, want := range []string{"Customer readiness and activation", "Needs configuration", "No guided activation record", "all retained months", "Alice &lt;script&gt;private&lt;/script&gt;", "Review readiness checks"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("overview page missing %q", want)
		}
	}
	if strings.Contains(w.Body.String(), "Alice <script>") {
		t.Fatal("customer name was not escaped")
	}
	for _, suffix := range []string{"?cursor=bad", "?cursor=" + uuid.NewString() + "&cursor=" + uuid.NewString(), "?tenant_id=" + tenant.ID} {
		call("GET", apiPath+suffix, key, false, "", 400)
	}
	raw, err := json.Marshal(api.CreatePlatformTenantAccessTokenRequest{Name: "overview-customer", Scopes: []string{api.ScopePlatformTenantOperationsRead}})
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
