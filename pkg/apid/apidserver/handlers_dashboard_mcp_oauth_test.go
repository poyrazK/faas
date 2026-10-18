package apidserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPBrowserOAuthSessionAndCleanCallback(t *testing.T) {
	handler, cookie, store, manager := newAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	previous := hostIdentitiesForUnseal
	hostIdentitiesForUnseal = func(context.Context) []*age.X25519Identity { return []*age.X25519Identity{identity} }
	t.Cleanup(func() { hostIdentitiesForUnseal = previous })
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "browser-own", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, Endpoint: "/mcp", IntervalSeconds: 30, TimeoutSeconds: 1, OAuth: &api.MCPCatalogObserverOAuthConfig{Guided: true, AuthorizationEndpoint: "https://provider.example/authorize", TokenEndpoint: "https://provider.example/token", ClientID: "reader", BrowserRedirectURI: "https://gregale.dev" + api.MCPCatalogOAuthBrowserCallbackPath}}}
	view, err := store.ConfigureMCPCatalogMonitor(t.Context(), account.ID, app.ID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRequest("GET", "/dashboard/mcp?app=browser-own", nil)
	get.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, get)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Reauthorize reader") {
		t.Fatal("browser action not rendered", page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == dashboardMCPRecoveryCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing recovery csrf")
	}
	post := func(slug, revision string, token bool) *httptest.ResponseRecorder {
		form := url.Values{"role": {"reader"}, "revision": {revision}, "action": {"begin"}}
		if token {
			form.Set("csrf_token", csrf.Value)
		}
		req := httptest.NewRequest("POST", "/dashboard/mcp/apps/"+slug+"/oauth", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(csrf)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(app.Slug, view.Config.Revision, false); rec.Code != 400 {
		t.Fatal("missing csrf accepted", rec.Code)
	}
	if rec := post(app.Slug, "stale", true); rec.Code != 409 {
		t.Fatal("stale revision accepted", rec.Code, rec.Body.String())
	}
	foreignAccount, err := store.CreateAccount(t.Context(), "browser-foreign@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: foreignAccount.ID, Slug: "browser-foreign", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if rec := post("browser-foreign", view.Config.Revision, true); rec.Code != 404 {
		t.Fatal("foreign app accepted", rec.Code)
	}
	begun := post(app.Slug, view.Config.Revision, true)
	if begun.Code != 303 {
		t.Fatal("authorization not started", begun.Code, begun.Body.String())
	}
	target, err := url.Parse(begun.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callbackState := target.Query().Get("state")
	var flowCookie *http.Cookie
	for _, c := range begun.Result().Cookies() {
		if c.Name == dashboardMCPOAuthCookie {
			flowCookie = c
		}
	}
	if flowCookie == nil || !flowCookie.Secure || !flowCookie.HttpOnly || flowCookie.SameSite != http.SameSiteLaxMode || flowCookie.Path != api.MCPCatalogOAuthBrowserCallbackPath {
		t.Fatal("unsafe flow cookie", flowCookie)
	}
	callback := func(query url.Values, sessionCookie, flow *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", api.MCPCatalogOAuthBrowserCallbackPath+"?"+query.Encode(), nil)
		if sessionCookie != nil {
			req.AddCookie(sessionCookie)
		}
		if flow != nil {
			req.AddCookie(flow)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	denied := url.Values{"state": {callbackState}, "error": {"access_denied"}, "error_description": {"PRIVATE_PROVIDER_DESCRIPTION"}}
	unauth := callback(url.Values{"state": {callbackState}, "code": {"PRIVATE_AUTHORIZATION_CODE"}}, nil, flowCookie)
	if unauth.Code != 302 || strings.Contains(unauth.Header().Get("Location"), "PRIVATE_AUTHORIZATION_CODE") || strings.Contains(unauth.Header().Get("Location"), callbackState) {
		t.Fatal("authorization leaked into login redirect", unauth.Code, unauth.Header().Get("Location"))
	}
	differentCookie := &http.Cookie{Name: sessionCookie, Value: issueDashboardTestCookie(t, store, manager, account.ID)}
	if rec := callback(denied, differentCookie, flowCookie); rec.Code != 400 {
		t.Fatal("different session completed callback", rec.Code)
	}
	duplicate := url.Values{"state": {callbackState, callbackState}, "error": {"access_denied"}}
	if rec := callback(duplicate, cookie, flowCookie); rec.Code != 400 {
		t.Fatal("duplicate callback state accepted", rec.Code)
	}
	raw, err := base64.RawURLEncoding.DecodeString(flowCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := manager.OpenForCSRF(raw)
	if err != nil {
		t.Fatal(err)
	}
	var flow dashboardMCPOAuthFlow
	if err = json.Unmarshal(plaintext, &flow); err != nil {
		t.Fatal(err)
	}
	flow.Expires = time.Now().Add(-time.Minute)
	payload, err := json.Marshal(flow)
	if err != nil {
		t.Fatal(err)
	}
	expiredValue, err := manager.SealForCSRF(payload)
	if err != nil {
		t.Fatal(err)
	}
	expired := callback(denied, cookie, &http.Cookie{Name: dashboardMCPOAuthCookie, Value: expiredValue})
	if expired.Code != 303 || !strings.Contains(expired.Header().Get("Location"), "authorization_expired") {
		t.Fatal("expired callback not explained", expired.Code, expired.Header())
	}
	resolved := callback(denied, cookie, flowCookie)
	if resolved.Code != 303 || !strings.Contains(resolved.Header().Get("Location"), "authorization_denied") || strings.Contains(resolved.Header().Get("Location"), "PRIVATE") {
		t.Fatal("denial not safe", resolved.Code, resolved.Header(), resolved.Body.String())
	}
	if resolved.Header().Get("Referrer-Policy") != "no-referrer" || resolved.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("callback privacy headers missing")
	}
	root, err := store.GetMCPCatalogMonitor(t.Context(), account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if current.ObserverStatus.OAuthFlow != nil || current.ObserverStatus.OAuth != nil {
		t.Fatal("denied authorization retained flow or installed grant")
	}
	replay := callback(denied, cookie, flowCookie)
	if replay.Code != 303 || !strings.Contains(replay.Header().Get("Location"), "authorization_failed") {
		t.Fatal("consumed callback replayed", replay.Code, replay.Header())
	}
}

func TestDashboardMCPOAuthRenewalRecovery(t *testing.T) {
	handler, cookie, store, _ := newAuthedDashboardServerFull(t)
	account, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateAccountPlan(t.Context(), account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "renewal-own", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	cfg := api.MCPCatalogMonitorConfig{Role: "reader", Enabled: true, Revision: uuid.NewString(), FreshnessSeconds: 90, Observer: &api.MCPCatalogObserverConfig{Enabled: true, Endpoint: "/mcp", IntervalSeconds: 30, TimeoutSeconds: 1, OAuth: &api.MCPCatalogObserverOAuthConfig{TokenEndpoint: "https://provider.example/token", ClientID: "reader", RefreshTokenSecretRef: "secret:REFRESH"}}}
	view, err := store.ConfigureMCPCatalogMonitor(t.Context(), account.ID, app.ID, cfg)
	if err != nil {
		t.Fatal(err)
	}
	setOutcome := func(snapshot api.MCPCatalogMonitorSnapshot, phase string) {
		t.Helper()
		job, e := store.ClaimMCPCatalogObserver(t.Context(), state.MCPCatalogObserverJob{AccountID: account.ID, AppID: app.ID, Role: "reader", Snapshot: snapshot})
		if e != nil {
			t.Fatal(e)
		}
		journal, fresh, e := store.BeginMCPCatalogOAuthRenewal(t.Context(), job, strings.Repeat("a", 64))
		if e != nil || !fresh {
			t.Fatal("renewal intent", e, fresh)
		}
		if e = store.RecordMCPCatalogOAuthRenewal(t.Context(), job, journal.ID, phase, nil); e != nil {
			t.Fatal(e)
		}
		_, e = store.CompleteMCPCatalogObserver(t.Context(), job, api.MCPCatalogReceipt{ID: uuid.NewString(), Role: "reader", Revision: snapshot.Config.Revision, State: "unavailable"}, "credential_unavailable", time.Minute)
		if e != nil {
			t.Fatal(e)
		}
	}
	setOutcome(view, "failed")
	get := httptest.NewRequest("GET", "/dashboard/mcp?app=renewal-own", nil)
	get.AddCookie(cookie)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, get)
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Queue renewal recovery") {
		t.Fatal("retry not offered", page.Code, page.Body.String())
	}
	var csrf *http.Cookie
	for _, c := range page.Result().Cookies() {
		if c.Name == dashboardMCPRecoveryCookie {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("missing csrf")
	}
	post := func(revision string) *httptest.ResponseRecorder {
		form := url.Values{"csrf_token": {csrf.Value}, "role": {"reader"}, "revision": {revision}, "action": {"retry_renewal"}}
		req := httptest.NewRequest("POST", "/dashboard/mcp/apps/renewal-own/oauth", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		req.AddCookie(csrf)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(view.Config.Revision); rec.Code != 303 || !strings.Contains(rec.Header().Get("Location"), "renewal_scheduled") {
		t.Fatal("retry not queued", rec.Code, rec.Body.String())
	}
	if rec := post(view.Config.Revision); rec.Code != 409 {
		t.Fatal("stale retry accepted", rec.Code)
	}
	root, err := store.GetMCPCatalogMonitor(t.Context(), account.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := api.CatalogMonitorRoleSnapshot(root, "reader")
	if current.ObserverStatus.Renewal == nil || current.ObserverStatus.Renewal.State != "failed" || current.Config.Revision == view.Config.Revision {
		t.Fatal("retry lost journal or failed CAS")
	}
	setOutcome(current, "reauthorization_required")
	if rec := post(current.Config.Revision); rec.Code != 409 {
		t.Fatal("uncertain exchange was retried", rec.Code, rec.Body.String())
	}
}
