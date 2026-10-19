package mcpcatalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

const browserTestRedirect = "https://console.example/dashboard/mcp/oauth/callback"

func browserFixture(t *testing.T, stores ...state.Store) *retirementFixture {
	t.Helper()
	var store state.Store = state.NewMemStore()
	if len(stores) > 0 {
		store = stores[0]
	}
	f := newRetirementFixture(t, store)
	view := f.view(t)
	view.Config.Observer.OAuth.BrowserRedirectURI = browserTestRedirect
	if _, err := f.store.(state.MCPCatalogMonitorStore).ConfigureMCPCatalogMonitor(t.Context(), f.acct.ID, f.app.ID, view.Config); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestBrowserOAuthSessionPKCEAndReplay(t *testing.T) {
	testBrowserOAuthSessionPKCEAndReplay(t, state.NewMemStore())
}
func TestMCPBrowserOAuthPostgres(t *testing.T) {
	testBrowserOAuthSessionPKCEAndReplay(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
func testBrowserOAuthSessionPKCEAndReplay(t *testing.T, store state.Store) {
	f := browserFixture(t, store)
	req := api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: f.view(t).Config.Revision, RedirectURI: browserTestRedirect}
	begun, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, req, browserTestRedirect, "session-a", false)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(begun.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	q := target.Query()
	if q.Get("redirect_uri") != browserTestRedirect || q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || q.Get("state") != begun.CallbackState {
		t.Fatal("invalid PKCE authorization", q)
	}
	view := f.view(t)
	if view.ObserverStatus.OAuthFlow == nil || view.ObserverStatus.OAuthFlow.BrowserSessionSHA256 == "" {
		t.Fatal("missing durable browser binding")
	}
	public, err := json.Marshal(api.CatalogObserverStatus(view, time.Now()))
	if err != nil || strings.Contains(string(public), "oauth_flow") || strings.Contains(string(public), "browser_session_sha256") {
		t.Fatal("public projection exposed flow", string(public), err)
	}
	_, err = f.store.(state.MCPCatalogObserverStore).ClaimMCPCatalogObserver(t.Context(), state.MCPCatalogObserverJob{AccountID: f.acct.ID, AppID: f.app.ID, Role: "reader", Snapshot: view})
	if !errors.Is(err, state.ErrMCPCatalogMonitorChanged) {
		t.Fatal("observer raced active authorization", err)
	}
	complete := api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Code: "PRIVATE_CODE"}
	if _, err = f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, complete, browserTestRedirect, "session-b", false); err == nil {
		t.Fatal("another session completed flow")
	}
	if _, err = f.runner.connectOAuth(t.Context(), f.acct.ID, f.app.ID, complete); err == nil {
		t.Fatal("CLI completed browser flow")
	}
	wrong := complete
	wrong.Role = "other"
	if _, err = f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, wrong, browserTestRedirect, "session-a", false); err == nil {
		t.Fatal("role binding bypassed")
	}
	wrong = complete
	wrong.Revision = "stale"
	if _, err = f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, wrong, browserTestRedirect, "session-a", false); err == nil {
		t.Fatal("revision binding bypassed")
	}
	out, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, complete, browserTestRedirect, "session-a", false)
	if err != nil || out.State != "connected" || f.tokens != 1 {
		t.Fatal("connection failed", out, err, f.tokens)
	}
	if _, err = f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, complete, browserTestRedirect, "session-a", false); err == nil || f.tokens != 1 {
		t.Fatal("exchange replayed", err, f.tokens)
	}
}

func TestBrowserOAuthDenialAndRedirectIsolation(t *testing.T) {
	f := browserFixture(t)
	req := api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: f.view(t).Config.Revision, RedirectURI: browserTestRedirect}
	if _, err := f.runner.connectOAuth(t.Context(), f.acct.ID, f.app.ID, req); err == nil {
		t.Fatal("HTTPS redirect accepted by CLI")
	}
	if _, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, req, "https://attacker.example/dashboard/mcp/oauth/callback", "session", false); err == nil {
		t.Fatal("unregistered console accepted")
	}
	begun, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, req, browserTestRedirect, "session", false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Denied: true}, browserTestRedirect, "session", false)
	if err != nil || out.State != "authorization_denied" || f.tokens != 0 || f.view(t).ObserverStatus.OAuthFlow != nil {
		t.Fatal("denial did not consume and clear flow", out, err)
	}
}

func TestBrowserOAuthUncertainExchangeIsNotRetried(t *testing.T) {
	f := browserFixture(t)
	begun, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: f.view(t).Config.Revision, RedirectURI: browserTestRedirect}, browserTestRedirect, "session", false)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.runner.Client = &http.Client{Transport: oauthRoundTripper(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("PRIVATE_PROVIDER_ERROR") })}
	req := api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Code: "PRIVATE_CODE"}
	out, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, req, browserTestRedirect, "session", false)
	if err == nil || out.State != "authorization_uncertain" || calls != 1 {
		t.Fatal("uncertain exchange not retained", out, err, calls)
	}
	if _, err = f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, req, browserTestRedirect, "session", false); err == nil || calls != 1 {
		t.Fatal("uncertain exchange replayed", err, calls)
	}
	records := f.history(t)
	if len(records) != 1 || records[0].State != "awaiting_issuance" {
		t.Fatal("lost reply missing durable grant record", records)
	}
}

func TestBrowserOAuthConcurrentCompletionExchangesOnce(t *testing.T) {
	f := browserFixture(t)
	begun, err := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: "reader", Revision: f.view(t).Config.Revision, RedirectURI: browserTestRedirect}, browserTestRedirect, "session", false)
	if err != nil {
		t.Fatal(err)
	}
	req := api.MCPCatalogOAuthRequest{Action: "complete", Role: "reader", Revision: begun.Revision, State: begun.CallbackState, Code: "PRIVATE_CODE"}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, e := f.runner.BrowserOAuth(t.Context(), f.acct.ID, f.app.ID, req, browserTestRedirect, "session", false)
			results <- e
		}()
	}
	succeeded := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			succeeded++
		}
	}
	if succeeded != 1 || f.tokens != 1 {
		t.Fatal("concurrent callbacks exchanged more than once", succeeded, f.tokens)
	}
}
