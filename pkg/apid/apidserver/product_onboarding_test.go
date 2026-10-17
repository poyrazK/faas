// ADR-1597: verified workspace onboarding, retained setup and activation evidence.
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
	"golang.org/x/net/html"
)

func onboardingForm(t *testing.T, body, suffix string) url.Values {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var found url.Values
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "form" {
			match := false
			for _, a := range n.Attr {
				if a.Key == "action" && strings.HasSuffix(a.Val, suffix) {
					match = true
				}
			}
			if match {
				found = url.Values{}
				var inputs func(*html.Node)
				inputs = func(p *html.Node) {
					if p.Type == html.ElementNode && p.Data == "input" {
						name, value := "", ""
						for _, a := range p.Attr {
							if a.Key == "name" {
								name = a.Val
							}
							if a.Key == "value" {
								value = a.Val
							}
						}
						if name != "" {
							found.Set(name, value)
						}
					}
					for c := p.FirstChild; c != nil; c = c.NextSibling {
						inputs(c)
					}
				}
				inputs(n)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if found == nil {
		t.Fatalf("missing %s form", suffix)
	}
	return found
}

func TestProductControlOnboardingHTTP(t *testing.T) {
	t.Setenv("FAAS_SCAN_SPOOL_ROOT", t.TempDir())
	ctx := t.Context()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "onboarding-owner@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "onboarding-product"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "onboarding-auth"})
	if err != nil {
		t.Fatal(err)
	}
	org, second, member := uuid.NewString(), uuid.NewString(), uuid.NewString()
	var role atomic.Value
	role.Store("owner")
	var authReads atomic.Int64
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/auth/v1/token" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["email"] != "workspace@example.com" || body["password"] != "workspace-password" {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "workspace.access.token", "refresh_token": "workspace.refresh.token", "expires_in": 600})
			return
		}
		authReads.Add(1)
		if r.URL.Path != "/authz/v1/orgs" || r.Header.Get("Authorization") != "Bearer workspace.access.token" {
			w.WriteHeader(401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []api.ProductOnboardingWorkspace{{ID: org, Slug: "acme", Name: "Acme workspace", Role: role.Load().(string)}, {ID: second, Slug: "beta", Name: "Beta workspace", Role: "admin"}, {ID: member, Slug: "read-only", Name: "Ordinary member", Role: "member"}}})
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
	owner, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CreateAPIKey(ctx, acct.ID, hash, "onboarding-owner", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	srv.ProductControl().AuthClient = upstream.Client()
	srv.ProductControl().AuthOrigin = func(_ context.Context, a state.Account, slug string) (state.App, string, error) {
		if a.ID != acct.ID || slug != auth.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return auth, upstream.URL, nil
	}
	_, err = store.PutProductCatalog(ctx, acct.ID, app.ID, api.ProductCatalogSpec{AuthApp: auth.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}, "pro": {Label: "Pro", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 20, UnitPriceMillicents: 10000}}}}})
	if err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	base := "/v1/apps/" + app.Slug + "/product/onboarding"
	browser := "/dashboard/apps/" + app.Slug + "/product-control/onboarding"
	apiCall := func(method, path string, body any, bearer string, status int) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "workspace.access.token") || strings.Contains(w.Body.String(), "workspace-password") {
			t.Fatal("credential leaked into response")
		}
		return w
	}
	apiCall("POST", base+"/workspaces", api.ProductOnboardingAuthRequest{AuthAccessToken: "workspace.access.token"}, owner, 404)
	srv.WithProductControlEnabled(true)
	for _, origin := range []string{"", "https://untrusted.example", "https://portal.example"} {
		r := httptest.NewRequest("POST", "https://portal.example"+base+"/workspaces", strings.NewReader(`{"auth_access_token":"workspace.access.token"}`))
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		r.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: sid})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		wanted := 403
		if origin == "https://portal.example" {
			wanted = 200
		}
		if w.Code != wanted {
			t.Fatalf("cookie API origin %q: %d want %d: %s", origin, w.Code, wanted, w.Body.String())
		}
	}
	apiCall("POST", base+"/workspaces", api.ProductOnboardingAuthRequest{AuthAccessToken: "workspace.access.token"}, "", 401)
	var workspaces api.ProductOnboardingWorkspaces
	w := apiCall("POST", base+"/workspaces", api.ProductOnboardingAuthRequest{AuthAccessToken: "workspace.access.token"}, owner, 200)
	if err = json.Unmarshal(w.Body.Bytes(), &workspaces); err != nil || len(workspaces.Workspaces) != 2 {
		t.Fatalf("workspace roles: %+v %v", workspaces, err)
	}
	req := api.ProductOnboardingRequest{ID: uuid.NewString(), ExternalRef: "acme", Name: "Acme customer", Plan: "starter", OrganizationID: org}
	w = apiCall("POST", base+"/preview", api.ProductOnboardingReviewRequest{Request: req, AuthAccessToken: "workspace.access.token"}, owner, 200)
	var preview api.ProductOnboardingPreview
	if err = json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	confirmation := api.ProductOnboardingConfirmation{Request: preview.Request, ExpectedHash: preview.Hash}
	role.Store("member")
	apiCall("POST", base+"/confirm", api.ProductOnboardingApplyRequest{Confirmation: confirmation, AuthAccessToken: "workspace.access.token"}, owner, 403)
	if _, err = store.ResolvePlatformTenantExternalRef(ctx, acct.ID, "acme"); err == nil {
		t.Fatal("revoked workspace membership still provisioned a customer")
	}
	role.Store("owner")
	w = apiCall("POST", base+"/confirm", api.ProductOnboardingApplyRequest{Confirmation: confirmation, AuthAccessToken: "workspace.access.token"}, owner, 200)
	var receipt api.ProductOnboardingReceipt
	if err = json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}

	// A real customer-boundary request, not the owner's setup session, supplies
	// access evidence. Auth and execution are fixtures, not native qualification.
	loginPath := "/v1/product-control/customer/" + app.Slug
	r := httptest.NewRequest("POST", "https://portal.example"+loginPath+"/session", strings.NewReader(`{"email":"workspace@example.com","password":"workspace-password"}`))
	r.Header.Set("Origin", "https://portal.example")
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("customer login: %d %s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	r = httptest.NewRequest("GET", "https://portal.example"+loginPath+"/product", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	r.Header.Set("X-Product-Organization", org)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("customer workspace: %d %s", w.Code, w.Body.String())
	}
	status, err := store.GetProductOnboarding(ctx, acct.ID, app.ID, receipt.ID)
	if err != nil || status.AccessVerifiedAt == nil || status.FirstSuccess != nil {
		t.Fatalf("access observation: %+v %v", status, err)
	}
	later := receipt.Customer
	later.Plan = "pro"
	if _, err = store.SetProductCustomer(ctx, acct.ID, app.ID, later); err != nil {
		t.Fatal(err)
	}
	role.Store("member")
	before := authReads.Load()
	apiCall("POST", base+"/confirm", api.ProductOnboardingApplyRequest{Confirmation: confirmation}, owner, 200)
	if authReads.Load() != before {
		t.Fatal("retained receipt recovery required an Auth session")
	}
	current, err := store.GetProductCustomer(ctx, acct.ID, app.ID, receipt.Tenant.ID)
	if err != nil || current != later {
		t.Fatalf("retry overwrote later terms: %+v %v", current, err)
	}
	apiCall("GET", base+"/"+receipt.ID, nil, owner, 200)

	t.Run("owner browser review and durable retry", func(t *testing.T) {
		jar := map[string]*http.Cookie{apidserver.SessionCookie: {Name: apidserver.SessionCookie, Value: sid}}
		call := func(method, path string, form url.Values, origin string, status int) *httptest.ResponseRecorder {
			t.Helper()
			body := ""
			if form != nil {
				body = form.Encode()
			}
			r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			for _, c := range jar {
				r.AddCookie(c)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != status {
				t.Fatalf("%s %s: %d want %d %s", method, path, w.Code, status, w.Body.String())
			}
			for _, cookie := range w.Result().Cookies() {
				if cookie.MaxAge < 0 {
					delete(jar, cookie.Name)
				} else {
					jar[cookie.Name] = cookie
				}
			}
			if strings.Contains(w.Body.String(), "workspace.access.token") || strings.Contains(w.Body.String(), "workspace-password") {
				t.Fatal("browser response leaked workspace credentials")
			}
			return w
		}
		page := call("GET", browser, nil, "", 200)
		login := onboardingForm(t, page.Body.String(), "/session")
		login.Set("email", "workspace@example.com")
		login.Set("password", "workspace-password")
		call("POST", browser+"/session", login, "https://elsewhere.example", 403)
		call("POST", browser+"/session", login, "https://portal.example", 303)
		connectionCookie := false
		for name, cookie := range jar {
			if strings.HasPrefix(name, "__Secure-gregale-pc-onboard-") {
				connectionCookie = true
				if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != browser || cookie.MaxAge > api.ProductControlOnboardingSessionSeconds {
					t.Fatalf("unsafe connection cookie: %+v", cookie)
				}
			}
		}
		if !connectionCookie {
			t.Fatal("missing scoped workspace connection")
		}
		page = call("GET", browser, nil, "", 200)
		if !strings.Contains(page.Body.String(), "Beta workspace") {
			t.Fatal("workspace selector missing")
		}
		review := onboardingForm(t, page.Body.String(), "/review")
		review.Set("external_ref", "beta")
		review.Set("name", "Beta customer")
		review.Set("plan", "starter")
		review.Set("organization", second)
		page = call("POST", browser+"/review", review, "https://portal.example", 200)
		if !strings.Contains(page.Body.String(), "0.25 EUR") && !strings.Contains(page.Body.String(), "EUR 0.25") {
			t.Fatalf("review omitted price: %s", page.Body.String())
		}
		confirm := onboardingForm(t, page.Body.String(), "/confirm")
		tampered := ownerConsoleCopy(confirm)
		tampered.Set("payload", confirm.Get("payload")+"x")
		call("POST", browser+"/confirm", tampered, "https://portal.example", 403)
		accepted := call("POST", browser+"/confirm", confirm, "https://portal.example", 303)
		location := accepted.Header().Get("Location")
		page = call("GET", location, nil, "", 200)
		if !strings.Contains(page.Body.String(), "Waiting for a customer") || !strings.Contains(page.Body.String(), "Open customer portal") {
			t.Fatal("activation or invitation guidance missing")
		}
		call("POST", browser+"/confirm", confirm, "https://portal.example", 303)
		tenant, e := store.ResolvePlatformTenantExternalRef(ctx, acct.ID, "beta")
		if e != nil {
			t.Fatal(e)
		}
		customer, e := store.GetProductCustomer(ctx, acct.ID, app.ID, tenant.ID)
		if e != nil || customer.AuthOrganizationID != second {
			t.Fatalf("browser setup: %+v %v", customer, e)
		}
		page = call("GET", browser, nil, "", 200)
		disconnect := onboardingForm(t, page.Body.String(), "/disconnect")
		call("POST", browser+"/disconnect", disconnect, "https://portal.example", 303)
		for name := range jar {
			if strings.HasPrefix(name, "__Secure-gregale-pc-onboard-") {
				t.Fatal("connection cookie survived disconnect")
			}
		}
	})

	srv.WithProductControlEnabled(false)
	for _, path := range []string{base + "/workspaces", base + "/preview", base + "/confirm"} {
		apiCall("POST", path, map[string]any{}, owner, 404)
	}
	apiCall("GET", base+"/"+receipt.ID, nil, owner, 404)
}
