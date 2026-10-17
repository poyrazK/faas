// ADR-1543: plan changes use live customer authority, reviewed terms and receipts.
package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlSelfServicePlansHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "plans@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "plans-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "plans-auth"})
	if err != nil {
		t.Fatal(err)
	}
	spec := api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 100, UnitPriceMillicents: 10000}}}}, Access: &api.ProductAccessPolicy{Roles: map[string][]api.ProductPermission{"owner": {api.ProductPermissionRunWork, api.ProductPermissionViewBilling}, "manager": {api.ProductPermissionManagePlan}, "billing": {api.ProductPermissionViewBilling}}}}
	initial, err := store.PutProductCatalog(ctx, acct.ID, app.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, "customer", "Customer", 100)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := store.CreatePlatformTenant(ctx, acct.ID, "other", "Other", 100)
	if err != nil {
		t.Fatal(err)
	}
	org, otherOrg := uuid.NewString(), uuid.NewString()
	for _, c := range []api.ProductCustomer{{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org}, {TenantID: other.ID, Plan: "starter", AuthOrganizationID: otherOrg}} {
		if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, c); err != nil {
			t.Fatal(err)
		}
	}
	owner, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	var role atomic.Value
	role.Store("manager")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/authz/v1/orgs" || r.Header.Get("Authorization") != "Bearer live.access.token" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": org, "name": "Customer", "role": role.Load().(string)}, {"id": otherOrg, "name": "Other", "role": "manager"}}})
	}))
	defer upstream.Close()
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid, err := apidserver.MintDashboardSession(ctx, store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "example.test", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "").WithProductControlEnabled(true)
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, a state.Account, slug string) (state.App, string, error) {
		if a.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return auth, upstream.URL, nil
	}
	handler := srv.Handler()
	base := "/v1/product-control/customer/" + app.Slug
	admin := "/v1/apps/" + app.Slug + "/product/plan-policy"
	call := func(method, path string, body any, credential, organization, origin string, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, e := json.Marshal(body)
		if e != nil {
			t.Fatal(e)
		}
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Product-Organization", organization)
		r.Header.Set("X-Product-Role", "owner")
		switch credential {
		case "customer":
			r.AddCookie(productAccessCookie(auth.ID))
		case "dashboard":
			r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sid})
		default:
			if credential != "" {
				r.Header.Set("Authorization", "Bearer "+credential)
			}
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w
	}
	const origin = "https://portal.example"
	choices := api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"pro"}, "pro": {"starter"}}}
	for _, credential := range []string{"", "customer"} {
		call("GET", admin, nil, credential, org, origin, 401)
	}
	for _, badOrigin := range []string{"", "https://foreign.example"} {
		call("POST", admin+"/preview", choices, "dashboard", org, badOrigin, 403)
	}
	call("POST", admin+"/preview", choices, "dashboard", org, origin, 200)
	call("POST", admin+"/preview", api.ProductPlanPolicy{Transitions: map[string][]string{"starter": {"missing"}}}, owner, org, origin, 400)
	w := call("POST", admin+"/preview", choices, owner, org, origin, 200)
	var policyReview api.ProductPlanPolicyPreview
	if err = json.Unmarshal(w.Body.Bytes(), &policyReview); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetProductCatalog(ctx, acct.ID, app.ID, "")
	if err != nil || current.Revision != initial.Revision {
		t.Fatal("policy preview mutated head", err)
	}
	call("PUT", admin, policyReview.Change, owner, org, origin, 200)
	call("PUT", admin, policyReview.Change, owner, org, origin, 200)
	w = call("GET", base+"/product", nil, "customer", org, origin, 200)
	var portal api.ProductPortal
	if err = json.Unmarshal(w.Body.Bytes(), &portal); err != nil {
		t.Fatal(err)
	}
	if !portal.Access.Permissions.ManagePlan || portal.Usage != nil || len(portal.Operations) != 0 || len(portal.Bindings) != 0 || portal.Catalog.Spec.SelfService != nil || portal.Catalog.Spec.Access != nil || portal.Customer.Revision != initial.Revision {
		t.Fatal("manager-only projection leaked unrelated data")
	}
	for _, path := range []string{"/billing", "/operations/" + uuid.NewString()} {
		call("GET", base+path, nil, "customer", org, origin, 403)
	}
	call("POST", base+"/start", map[string]any{}, "customer", org, origin, 403)
	w = call("GET", base+"/plans", nil, "customer", org, origin, 200)
	var options api.ProductPlanOptions
	if err = json.Unmarshal(w.Body.Bytes(), &options); err != nil || len(options.Offers) != 1 || options.Offers[0].Features[0].UnitPriceMillicents != 10000 {
		t.Fatalf("commercial choices: %+v %v", options, err)
	}
	for _, deniedRole := range []string{"owner", "billing"} {
		role.Store(deniedRole)
		call("GET", base+"/plans", nil, "customer", org, origin, 403)
	}
	role.Store("manager")
	selection := api.ProductPlanSelection{ID: uuid.NewString(), Plan: "pro", Revision: options.HeadRevision}
	call("POST", base+"/plans/preview", selection, "customer", org, "https://foreign.example", 403)
	call("POST", base+"/plans/preview", map[string]string{"id": selection.ID, "plan": "pro", "tenant_id": other.ID}, "customer", org, origin, 400)
	w = call("POST", base+"/plans/preview", selection, "customer", org, origin, 200)
	var preview api.ProductPlanChangePreview
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	confirm := api.ProductPlanChangeConfirmation{Request: preview.Request, ExpectedHash: preview.Hash}
	bad := confirm
	bad.ExpectedHash = strings.Repeat("0", 64)
	call("POST", base+"/plans/confirm", bad, "customer", org, origin, 409)
	call("POST", base+"/plans/confirm", confirm, "customer", org, origin, 200)
	call("POST", base+"/plans/confirm", confirm, "customer", org, origin, 200)
	w = call("GET", base+"/plans/changes/"+selection.ID, nil, "customer", org, origin, 200)
	var receipt api.ProductPlanChangeReceipt
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil || receipt.Preview.AfterCustomer.Plan != "pro" {
		t.Fatal("receipt missing", err)
	}
	call("GET", base+"/plans/changes/"+selection.ID, nil, "customer", otherOrg, origin, 404)
	w = call("GET", base+"/plans/changes", nil, "customer", org, origin, 200)
	var history api.ProductPlanChanges
	if err = json.Unmarshal(w.Body.Bytes(), &history); err != nil || len(history.Changes) != 1 {
		t.Fatal("retry duplicated history", err)
	}
	// The identity is a live Auth session, never a platform service credential.
	tokenResponse := call("POST", "/v1/account/platform-tenants/"+tenant.ID+"/access-tokens", api.CreatePlatformTenantAccessTokenRequest{Name: "service", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}}, owner, org, origin, 201)
	var token api.CreatePlatformTenantAccessTokenResponse
	if err = json.Unmarshal(tokenResponse.Body.Bytes(), &token); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/plans/confirm", confirm, token.Token, org, origin, 401)
	call("GET", admin, nil, token.Token, org, origin, 403)
	// Owner browser publication requires its signed review as well as Origin.
	console := "/dashboard/apps/" + app.Slug + "/product-control/plan-policy"
	jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
	browser := func(method, path string, form url.Values, requestOrigin string, status int) *httptest.ResponseRecorder {
		t.Helper()
		body := ""
		if form != nil {
			body = form.Encode()
		}
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", requestOrigin)
		for _, cookie := range jar {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("console %s: %d want %d: %s", path, w.Code, status, w.Body.String())
		}
		for _, cookie := range w.Result().Cookies() {
			jar[cookie.Name] = cookie
		}
		return w
	}
	page := browser("GET", console, nil, "", 200)
	form := onboardingForm(t, page.Body.String(), "/plan-policy/review")
	form.Set("action", "close")
	form.Del("source")
	form.Del("target")
	missing := ownerConsoleCopy(form)
	missing.Del("csrf_token")
	browser("POST", console+"/review", missing, origin, 403)
	browser("POST", console+"/review", form, "https://foreign.example", 403)
	reviewPage := browser("POST", console+"/review", form, origin, 200)
	policyConfirm := onboardingForm(t, reviewPage.Body.String(), "/plan-policy/confirm")
	tampered := ownerConsoleCopy(policyConfirm)
	tampered.Set("payload", policyConfirm.Get("payload")+"A")
	browser("POST", console+"/confirm", tampered, origin, 400)
	browser("POST", console+"/confirm", policyConfirm, origin, 200)
	browser("POST", console+"/confirm", policyConfirm, origin, 200)
	call("PUT", admin, policyReview.Change, owner, org, origin, 409)
	// Old confirmations recover history while later owner decisions remain current.
	call("POST", base+"/plans/confirm", confirm, "customer", org, origin, 200)
	call("POST", base+"/plans/preview", api.ProductPlanSelection{ID: uuid.NewString(), Plan: "starter"}, "customer", org, origin, 409)
	role.Store("billing")
	call("GET", base+"/plans/changes/"+selection.ID, nil, "customer", org, origin, 403)
	role.Store("manager")
	srv.WithProductControlEnabled(false)
	for _, route := range []struct{ method, path string }{{"GET", admin}, {"POST", admin + "/preview"}, {"PUT", admin}, {"GET", base + "/plans"}, {"POST", base + "/plans/preview"}, {"POST", base + "/plans/confirm"}, {"GET", base + "/plans/changes"}, {"GET", base + "/plans/changes/" + selection.ID}, {"GET", "/v1/product-control/portal-plans.js"}} {
		call(route.method, route.path, nil, "customer", org, origin, 404)
	}
	browser("GET", console, nil, "", 404)
}
