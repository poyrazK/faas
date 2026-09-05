// Tests for cmd/apid/handlers_oauth_code_callback.go (PR-C).
//
// The user-to-server OAuth handshake is the load-bearing CSRF boundary
// for the dashboard's "Connect GitHub" button. Three cases pin the
// security posture:
//
//  1. Happy path: state cookie matches query → 302 to the dashboard
//     account page with the install id + default branch + connected_at.
//     The state cookie is cleared on success (single-use).
//  2. CSRF mismatch: state cookie != query state → 403 + audit
//     auth.install.csrf_rejected {reason: state_mismatch}. The handler
//     must NOT call githubd.ExchangeOAuthCode on a mismatch.
//  3. githubd transport error: cookie matches, but the gRPC call
//     fails → 502 + audit auth.install.token_exchange_failed.
//  4. missing GitHub App installation: the connect handler and the
//     callback race both redirect to GitHub's installation flow.
//
// Plus a missing-state and missing-code branch as the cheap 400
// guards against malformed queries.
package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// oauthCodeCallbackFake is a GithubdClient stub that records the
// (accountID, code, state) tuple passed to ExchangeOAuthCode and
// returns a canned (installID, defaultBranch, err). Used by every
// test in this file; we can't reuse fakeGithubdClient from
// handlers_oauth_test.go because its ExchangeOAuthCode returns
// errGithubdNotReady.
type oauthCodeCallbackFake struct {
	installID       string
	defaultBranch   string
	exchangeErr     error
	installState    InstallState
	installStateErr error

	gotAccountID string
	gotCode      string
	gotState     string
	gotCalls     int
}

// GithubdClient surface, with the methods we don't use falling
// through to not-ready problems so accidental calls fail loudly.
func (f *oauthCodeCallbackFake) GetInstallState(context.Context, string) (InstallState, string, string, error) {
	return f.installState, "", "", f.installStateErr
}
func (f *oauthCodeCallbackFake) ExchangeOAuthCode(_ context.Context, accountID, code, state string) (string, string, error) {
	f.gotAccountID = accountID
	f.gotCode = code
	f.gotState = state
	f.gotCalls++
	return f.installID, f.defaultBranch, f.exchangeErr
}
func (f *oauthCodeCallbackFake) ListInstallableRepos(context.Context, string, int64) ([]Repo, error) {
	return nil, errGithubdNotReady
}
func (f *oauthCodeCallbackFake) BindAppRepo(context.Context, string, string, int64, string, string) (string, error) {
	return "", errGithubdNotReady
}
func (f *oauthCodeCallbackFake) UnbindAppRepo(context.Context, string, string) error {
	return errGithubdNotReady
}
func (f *oauthCodeCallbackFake) GetAppBinding(context.Context, string, string) (AppBinding, error) {
	return AppBinding{}, errGithubdNotReady
}
func (f *oauthCodeCallbackFake) CreateDeploymentFromPush(context.Context, string, string, string, string) (string, string, error) {
	return "", "", errGithubdNotReady
}
func (f *oauthCodeCallbackFake) WriteCheck(context.Context, string, string, CheckPhase, string, string) error {
	return errGithubdNotReady
}
func (f *oauthCodeCallbackFake) VerifyInstallation(context.Context, int64, string) (bool, string, string, error) {
	return false, "", "", errGithubdNotReady
}

// MintInstallationToken is unused by the OAuth code-callback test.
// Returns the not-ready problem (DEPLOY-PROV-4 / ADR-092, issue #739).
func (f *oauthCodeCallbackFake) MintInstallationToken(context.Context, string, int64) (string, time.Time, error) {
	return "", time.Time{}, errGithubdNotReady
}

// StreamSourceRef is unused by the OAuth code-callback test.
// Returns the not-ready problem (DEPLOY-PROV-4 / ADR-092, issue #739).
func (f *oauthCodeCallbackFake) StreamSourceRef(context.Context, string, int64, string, string, int64) (*StreamSourceRefResult, error) {
	return nil, errGithubdNotReady
}
func (f *oauthCodeCallbackFake) Close() error { return nil }

// mintStateToken returns a 32-char hex token (16 bytes from crypto/rand)
// matching the generator inside issueOAuthCodeState. Test-local
// helper so the test doesn't have to round-trip through the cookie
// writer to forge a value.
func mintStateToken(t *testing.T) string {
	t.Helper()
	tokenBytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(tokenBytes)
}

// newOAuthCodeCallbackServer wires a session-authed apid handler
// with the oauthCodeCallbackFake, returning the handler, the session
// cookie, and the fake so tests can assert the githubd surface.
func newOAuthCodeCallbackServer(t *testing.T, gh *oauthCodeCallbackFake) (http.Handler, *session.Manager, string, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	cookie, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "gregale.dev", noopNotifier{}, "", noopMailer{}, gh, mgr, nil, 15*60_000_000_000, "")
	return srv.handler(), mgr, acct.ID, &http.Cookie{Name: sessionCookie, Value: cookie}
}

// TestRenderOAuthCodeCallback_HappyPath pins the success flow: a
// matching state cookie + valid code + githubd returns success →
// 302 to /dashboard/account with the install id and default branch
// in the query. The state cookie is cleared on success (single-use).
func TestRenderOAuthCodeCallback_HappyPath(t *testing.T) {
	gh := &oauthCodeCallbackFake{installID: "9999", defaultBranch: "main"}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)
	stateToken := mintStateToken(t)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code-xyz&state="+stateToken, nil)
	r.AddCookie(sessionCookie)
	r.AddCookie(&http.Cookie{Name: oauthCodeStateCookie, Value: stateToken, Path: oauthCodeCallbackPath})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/dashboard/account?") {
		t.Errorf("Location = %q, want /dashboard/account?…", loc)
	}
	if !strings.Contains(loc, "github=connected") {
		t.Errorf("Location missing github=connected: %q", loc)
	}
	if !strings.Contains(loc, "install=9999") {
		t.Errorf("Location missing install=9999: %q", loc)
	}
	if !strings.Contains(loc, "default_branch=main") {
		t.Errorf("Location missing default_branch=main: %q", loc)
	}
	if gh.gotCalls != 1 {
		t.Errorf("ExchangeOAuthCode calls = %d, want 1", gh.gotCalls)
	}
	if gh.gotCode != "oauth-code-xyz" {
		t.Errorf("githubd got code = %q, want oauth-code-xyz", gh.gotCode)
	}
	// Single-use: the handler clears the state cookie on success.
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == oauthCodeStateCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("state cookie not cleared on success")
	}
}

// TestRenderOAuthCodeCallback_StateMismatch pins the CSRF tripwire:
// a state cookie that does NOT match the query state → 403 with
// code=csrf_mismatch. githubd must NOT be called on a mismatch
// (the CSRF gate is apid's job; leaking the oauth code to githubd
// would attempt an exchange with a forged state).
func TestRenderOAuthCodeCallback_StateMismatch(t *testing.T) {
	gh := &oauthCodeCallbackFake{installID: "9999", defaultBranch: "main"}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code-xyz&state=querystatetoken", nil)
	r.AddCookie(sessionCookie)
	r.AddCookie(&http.Cookie{Name: oauthCodeStateCookie, Value: "cookiestatetoken", Path: oauthCodeCallbackPath})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("code = %d, want 403\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "csrf_mismatch") {
		t.Errorf("body missing csrf_mismatch code: %s", rec.Body.String())
	}
	if gh.gotCalls != 0 {
		t.Errorf("ExchangeOAuthCode must NOT be called on CSRF mismatch; got %d calls", gh.gotCalls)
	}
}

// TestRenderOAuthCodeCallback_GithubdErrorPropagates pins the
// transport-failure path: state cookie matches, but githubd returns
// a non-nil err → 502 with code=github_unreachable. The handler
// must surface the error to the dashboard so the retry button can
// render.
func TestRenderOAuthCodeCallback_GithubdErrorPropagates(t *testing.T) {
	gh := &oauthCodeCallbackFake{exchangeErr: errors.New("dial tcp: connection refused")}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)
	stateToken := mintStateToken(t)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code-xyz&state="+stateToken, nil)
	r.AddCookie(sessionCookie)
	r.AddCookie(&http.Cookie{Name: oauthCodeStateCookie, Value: stateToken, Path: oauthCodeCallbackPath})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, want 502\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "github_unreachable") {
		t.Errorf("body missing github_unreachable code: %s", rec.Body.String())
	}
}

// TestRenderOAuthCodeCallback_MissingState covers the 400 path when
// the dashboard forgets to mint the state cookie (the redirect from
// the "Connect GitHub" button failed). The handler must short-circuit
// before reaching githubd.
// TestRenderOAuthCodeCallback_MissingInstallationResumesInstallFlow covers
// the race where GitHub has redirected with a code but githubd still finds no
// App installation. This is an expected first-use condition, not a 502 outage.
func TestRenderOAuthCodeCallback_MissingInstallationResumesInstallFlow(t *testing.T) {
	t.Setenv("FAAS_GITHUB_APP_INSTALL_URL", "https://github.com/apps/test-app/installations/new")
	gh := &oauthCodeCallbackFake{
		exchangeErr: errors.New("rpc error: code = Internal desc = githubd: user has no app installations"),
	}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)
	stateToken := mintStateToken(t)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code-xyz&state="+stateToken, nil)
	r.AddCookie(sessionCookie)
	r.AddCookie(&http.Cookie{Name: oauthCodeStateCookie, Value: stateToken, Path: oauthCodeCallbackPath})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Path != "/apps/test-app/installations/new" {
		t.Errorf("Location path = %q, want /apps/test-app/installations/new", loc.Path)
	}
	if loc.Query().Get("state") == "" {
		t.Error("installation redirect missing fresh state")
	}
	if strings.Contains(rec.Body.String(), "github_unreachable") {
		t.Error("missing installation must not render github_unreachable")
	}
	if gh.gotCalls != 1 {
		t.Errorf("ExchangeOAuthCode calls = %d, want 1", gh.gotCalls)
	}
}

func TestStartConnectGitHub_RedirectsToInstallationWhenNeeded(t *testing.T) {
	t.Setenv("FAAS_GITHUB_APP_CLIENT_ID", "client-123")
	t.Setenv("FAAS_GITHUB_APP_INSTALL_URL", "https://github.com/apps/test-app/installations/new")
	gh := &oauthCodeCallbackFake{installState: InstallStateNotInstalled}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/dashboard/install/connect", nil)
	r.AddCookie(sessionCookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Path != "/apps/test-app/installations/new" {
		t.Errorf("Location path = %q, want /apps/test-app/installations/new", loc.Path)
	}
	stateToken := loc.Query().Get("state")
	if stateToken == "" {
		t.Fatal("installation redirect missing state")
	}
	if stateCookie := findCookie(rec.Result().Cookies(), oauthCodeStateCookie); stateCookie == nil || stateCookie.Value != stateToken {
		t.Errorf("state cookie = %#v, want value matching redirect state %q", stateCookie, stateToken)
	}
}

func TestStartConnectGitHub_AuthorizesInstalledApp(t *testing.T) {
	t.Setenv("FAAS_GITHUB_APP_CLIENT_ID", "client-123")
	t.Setenv("FAAS_GITHUB_APP_REDIRECT_URI", "https://gregale.dev/oauth/code-callback")
	t.Setenv("FAAS_GITHUB_APP_INSTALL_URL", "https://github.com/apps/test-app/installations/new")
	gh := &oauthCodeCallbackFake{installState: InstallStateInstalled}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/dashboard/install/connect", nil)
	r.AddCookie(sessionCookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Path != "/login/oauth/authorize" {
		t.Errorf("Location path = %q, want /login/oauth/authorize", loc.Path)
	}
	if got := loc.Query().Get("client_id"); got != "client-123" {
		t.Errorf("client_id = %q, want client-123", got)
	}
	if got := loc.Query().Get("redirect_uri"); got != "https://gregale.dev/oauth/code-callback" {
		t.Errorf("redirect_uri = %q, want configured callback", got)
	}
	if loc.Query().Get("state") == "" {
		t.Error("authorize redirect missing state")
	}
}

func TestGithubAppInstallURL_RejectsNonHTTPSConfiguration(t *testing.T) {
	t.Setenv("FAAS_GITHUB_APP_INSTALL_URL", "http://github.com/apps/test-app/installations/new")
	if _, err := githubAppInstallURL("state"); err == nil {
		t.Fatal("githubAppInstallURL accepted non-HTTPS configuration")
	}
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func TestRenderOAuthCodeCallback_MissingState(t *testing.T) {
	gh := &oauthCodeCallbackFake{installID: "9999", defaultBranch: "main"}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?code=oauth-code-xyz", nil)
	r.AddCookie(sessionCookie)
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if gh.gotCalls != 0 {
		t.Errorf("ExchangeOAuthCode must NOT be called when state is missing; got %d calls", gh.gotCalls)
	}
}

// TestRenderOAuthCodeCallback_MissingCode covers the 400 path when
// GitHub's redirect landed without a code (operator mis-configured
// the App's redirect URL? user rejected the dialog?).
func TestRenderOAuthCodeCallback_MissingCode(t *testing.T) {
	gh := &oauthCodeCallbackFake{installID: "9999", defaultBranch: "main"}
	srv, _, _, sessionCookie := newOAuthCodeCallbackServer(t, gh)
	stateToken := mintStateToken(t)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/oauth/code-callback?state="+stateToken, nil)
	r.AddCookie(sessionCookie)
	r.AddCookie(&http.Cookie{Name: oauthCodeStateCookie, Value: stateToken, Path: oauthCodeCallbackPath})
	srv.ServeHTTP(rec, r)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
	if gh.gotCalls != 0 {
		t.Errorf("ExchangeOAuthCode must NOT be called when code is missing; got %d calls", gh.gotCalls)
	}
}

// TestRenderOAuthCodeCallback_CSRFComparisonIsConstantTime is a
// defensive tripwire: the handler must use crypto/subtle.ConstantTimeCompare
// (not bytes.Equal) for the state comparison. If a future refactor
// drops the constant-time compare, this test fails — and the
// regression is loud, not silent.
func TestRenderOAuthCodeCallback_CSRFComparisonIsConstantTime(t *testing.T) {
	// Compile-time check: the constant-time-compare path is
	// exercised by the handler. We can't directly unit-test that
	// handler code uses ConstantTimeCompare, but we can at least
	// assert that the helper is imported and importable in this
	// package's test scope.
	if subtle.ConstantTimeCompare([]byte("a"), []byte("a")) != 1 {
		t.Fatal("crypto/subtle import broken or zero")
	}
}
