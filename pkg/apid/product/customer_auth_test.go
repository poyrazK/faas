package product

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type customerTestHost struct{ store *state.MemStore }

func (h customerTestHost) Store() state.Store                                         { return h.store }
func (h customerTestHost) EmitAudit(context.Context, string, *string, map[string]any) {}
func (h customerTestHost) LoadApp(w http.ResponseWriter, r *http.Request, account state.Account, slug string) (state.App, bool) {
	app, err := h.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != account.ID {
		respond(w, nil, state.ErrNotFound)
		return state.App{}, false
	}
	return app, true
}

func TestProductCustomerAuthBridge(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "customer-login@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "customer-product"})
	if err != nil {
		t.Fatal(err)
	}
	authApp, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "customer-auth"})
	if err != nil {
		t.Fatal(err)
	}
	tenant, _, err := store.CreatePlatformTenant(ctx, account.ID, "alice", "Alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.NewString()
	spec := api.ProductCatalogSpec{AuthApp: authApp.Slug, Currency: "EUR", Features: map[string]api.ProductFeature{"export": {Label: "Export", Operation: &api.ProductOperationBinding{Name: "export", Scope: api.DefaultEnvScope}}}, Plans: map[string]api.ProductPlan{"starter": {Label: "Starter", Features: map[string]api.ProductFeatureGrant{"export": {MonthlyStarts: 10, UnitPriceMillicents: 25000}}}}}
	if _, err := store.PutProductCatalog(ctx, account.ID, app.ID, spec); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetProductCustomer(ctx, account.ID, app.ID, api.ProductCustomer{TenantID: tenant.ID, Plan: "starter", AuthOrganizationID: org}); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	var tokenCalls, logoutCalls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/v1/token":
			tokenCalls.Add(1)
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.URL.Query().Get("grant_type") == "password" && (body["email"] != "alice@example.com" || body["password"] != "private-password") {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"code":"invalid_credentials","detail":"must not be reflected"}`))
				return
			}
			if r.URL.Query().Get("grant_type") == "refresh_token" && body["refresh_token"] != "refresh-secret" {
				t.Error("incorrect refresh credential")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access.secret.jwt", "refresh_token": "refresh-secret", "expires_in": 600})
		case "/authz/v1/orgs":
			if r.Header.Get("Authorization") != "Bearer access.secret.jwt" || revoked.Load() {
				w.WriteHeader(401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"organizations": []map[string]string{{"id": org, "name": "Alice", "role": "owner"}}})
		case "/auth/v1/logout":
			if r.Header.Get("Authorization") != "Bearer access.secret.jwt" || r.URL.Query().Get("scope") != "local" {
				t.Error("incorrect logout authority")
			}
			logoutCalls.Add(1)
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected Auth endpoint %s", r.URL)
			w.WriteHeader(404)
		}
	}))
	defer upstream.Close()
	service := Service{Host: customerTestHost{store}, Enabled: true, AuthClient: upstream.Client(), AuthOrigin: func(_ context.Context, owner state.Account, slug string) (state.App, string, error) {
		if owner.ID != account.ID || slug != authApp.Slug {
			return state.App{}, "", state.ErrNotFound
		}
		return authApp, upstream.URL, nil
	}}
	call := func(method, path, origin, body string, cookies []*http.Cookie, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "https://portal.example"+path, strings.NewReader(body))
		r.SetPathValue("slug", app.Slug)
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		service.Gate(handler)(w, r)
		return w
	}
	base := "/v1/product-control/customer/" + app.Slug
	for _, origin := range []string{"", "null", "https://foreign.example", "http://portal.example"} {
		if w := call("POST", base+"/session", origin, `{"email":"alice@example.com","password":"private-password"}`, nil, service.CustomerLogin); w.Code != 403 {
			t.Fatalf("cross-origin login %q: %d", origin, w.Code)
		}
	}
	if tokenCalls.Load() != 0 {
		t.Fatal("origin failure reached Auth")
	}
	bad := call("POST", base+"/session", "https://portal.example", `{"email":"alice@example.com","password":"wrong"}`, nil, service.CustomerLogin)
	if bad.Code != 401 || strings.Contains(bad.Body.String(), "must not be reflected") {
		t.Fatalf("login error leak: %d %s", bad.Code, bad.Body.String())
	}
	login := call("POST", base+"/session", "https://portal.example", `{"email":"alice@example.com","password":"private-password"}`, nil, service.CustomerLogin)
	if login.Code != 200 {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 2 || strings.Contains(login.Body.String(), "secret") {
		t.Fatalf("session credentials exposed: %s", login.Body.String())
	}
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != base+"/" || !strings.HasPrefix(cookie.Name, "__Secure-") {
			t.Fatalf("unsafe session cookie: %+v", cookie)
		}
	}
	workspaces := call("GET", base+"/workspaces", "", "", cookies, service.CustomerWorkspaces)
	if workspaces.Code != 200 || !strings.Contains(workspaces.Body.String(), tenant.ID) {
		t.Fatalf("identity mapping: %d %s", workspaces.Code, workspaces.Body.String())
	}
	reached := false
	protected := service.Customer("", func(w http.ResponseWriter, r *http.Request, owner state.Account) {
		reached = true
		id, ok := httpkit.PlatformTenantSelfID(w, r)
		if !ok || id != tenant.ID || owner.ID != account.ID {
			t.Error("forged tenant identity")
		}
		w.WriteHeader(204)
	})
	invoke := func(selectedOrg string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://portal.example"+base+"/product", nil)
		r.SetPathValue("slug", app.Slug)
		r.Header.Set("X-Product-Organization", selectedOrg)
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		protected(w, r)
		return w
	}
	if w := invoke(uuid.NewString()); w.Code != 403 || reached {
		t.Fatalf("foreign organization selected a customer: %d", w.Code)
	}
	if w := invoke(org); w.Code != 204 || !reached {
		t.Fatalf("live membership failed: %d %s", w.Code, w.Body.String())
	}
	revoked.Store(true)
	reached = false
	if w := invoke(org); w.Code != 401 || reached {
		t.Fatalf("revoked session was cached: %d", w.Code)
	}
	revoked.Store(false)
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	if w := invoke(org); w.Code != 403 || reached {
		t.Fatalf("suspended tenant retained access: %d", w.Code)
	}
	if _, err := store.SetPlatformTenantStatus(ctx, account.ID, tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	refresh := call("POST", base+"/session/refresh", "https://portal.example", "", cookies, service.CustomerRefresh)
	if refresh.Code != 200 || len(refresh.Result().Cookies()) != 2 {
		t.Fatalf("session refresh: %d %s", refresh.Code, refresh.Body.String())
	}
	// An expired access cookie still needs to revoke its live refresh session.
	logout := call("DELETE", base+"/session", "https://portal.example", "", cookies[1:], service.CustomerLogout)
	if logout.Code != 204 || logoutCalls.Load() != 1 {
		t.Fatalf("logout: %d %s", logout.Code, logout.Body.String())
	}
	for _, cookie := range logout.Result().Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatal("logout retained cookie")
		}
	}
	service.AuthOrigin = func(context.Context, state.Account, string) (state.App, string, error) {
		return state.App{}, "", state.ErrNotFound
	}
	unavailableLogout := call("DELETE", base+"/session", "https://portal.example", "", cookies, service.CustomerLogout)
	if len(unavailableLogout.Result().Cookies()) != 2 {
		t.Fatal("Auth outage prevented local cookie deletion")
	}
	for _, cookie := range unavailableLogout.Result().Cookies() {
		if cookie.MaxAge != -1 {
			t.Fatal("Auth outage retained local session")
		}
	}
	service.Enabled = false
	if w := call("POST", base+"/session", "https://portal.example", "", nil, service.CustomerLogin); w.Code != 404 {
		t.Fatalf("disabled login: %d", w.Code)
	}
}

func TestProductCustomerAuthRejectsRedirectsAndOversizeResponses(t *testing.T) {
	var reached atomic.Bool
	destination := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached.Store(true) }))
	defer destination.Close()
	for _, mode := range []string{"redirect", "oversize", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch mode {
				case "redirect":
					http.Redirect(w, r, destination.URL, 307)
				case "oversize":
					_, _ = w.Write([]byte(strings.Repeat(" ", api.ProductControlAuthResponseBytes+1)))
				case "malformed":
					_, _ = w.Write([]byte("{}{}"))
				}
			}))
			defer upstream.Close()
			s := Service{AuthClient: upstream.Client()}
			var out map[string]any
			err := s.authCall(t.Context(), customerRealm{origin: upstream.URL}, "GET", "/authz/v1/orgs", "private-secret", nil, &out)
			var failure customerAuthFailure
			if !errors.As(err, &failure) || failure.status != 503 || reached.Load() {
				t.Fatalf("unsafe auth response accepted: %v", err)
			}
		})
	}
}
