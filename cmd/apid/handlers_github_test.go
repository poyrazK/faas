package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	"github.com/onebox-faas/faas/pkg/oauthpkce"
	"github.com/onebox-faas/faas/pkg/state"
)

// fakeGitHubAPI impersonates api.github.com + github.com for the OAuth
// dashboard login flow. The three endpoints the handler hits are:
//
//	POST https://github.com/login/oauth/access_token
//	GET  https://api.github.com/user
//	GET  https://api.github.com/user/emails
//
// The RoundTripper below rewrites those three URLs to the httptest
// server; everything else passes through DefaultTransport so the rest
// of the test process keeps working.
type fakeGitHubAPI struct {
	server *httptest.Server

	// token body + status returned for the access_token exchange.
	tokenStatus int
	tokenBody   []byte

	// user status + body returned for /user.
	userStatus int
	userBody   []byte

	// emails status + body returned for /user/emails.
	emailsStatus int
	emailsBody   []byte

	// acceptSeen counts how many requests to access_token carried
	// Accept: application/json. Used by the Accept-header regression
	// (case #13).
	acceptSeen *atomic.Int32
	// verifierSeen counts token exchanges carrying the RFC 7636
	// code_verifier form field.
	verifierSeen *atomic.Int32
}

func newFakeGitHubAPI(t *testing.T) *fakeGitHubAPI {
	t.Helper()
	f := &fakeGitHubAPI{
		tokenStatus:  http.StatusOK,
		tokenBody:    []byte(`{"access_token":"fake_gh_token","scope":"read:user,user:email","token_type":"bearer"}`),
		userStatus:   http.StatusOK,
		emailsStatus: http.StatusOK,
		acceptSeen:   &atomic.Int32{},
		verifierSeen: &atomic.Int32{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			if strings.EqualFold(r.Header.Get("Accept"), "application/json") {
				f.acceptSeen.Add(1)
			}
			if err := r.ParseForm(); err == nil && r.Form.Get("code_verifier") != "" {
				f.verifierSeen.Add(1)
			}
			w.WriteHeader(f.tokenStatus)
			_, _ = w.Write(f.tokenBody)
		case r.URL.Path == "/user" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			w.WriteHeader(f.userStatus)
			_, _ = w.Write(f.userBody)
		case r.URL.Path == "/user/emails" && strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "):
			w.WriteHeader(f.emailsStatus)
			_, _ = w.Write(f.emailsBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// rewriteTo is a RoundTripper that redirects only the three GitHub
// outbound URLs to the impersonator. Other URLs pass through to the
// wrapped RoundTripper.
type rewriteTo struct {
	origin *url.URL
	next   http.RoundTripper
}

func (r *rewriteTo) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case req.URL.Host == "github.com" && req.URL.Path == "/login/oauth/access_token":
		req.URL.Scheme = r.origin.Scheme
		req.URL.Host = r.origin.Host
		req.URL.Path = "/login/oauth/access_token"
	case req.URL.Host == "api.github.com" && req.URL.Path == "/user":
		req.URL.Scheme = r.origin.Scheme
		req.URL.Host = r.origin.Host
		req.URL.Path = "/user"
	case req.URL.Host == "api.github.com" && req.URL.Path == "/user/emails":
		req.URL.Scheme = r.origin.Scheme
		req.URL.Host = r.origin.Host
		req.URL.Path = "/user/emails"
	default:
		return r.next.RoundTrip(req)
	}
	return r.next.RoundTrip(req)
}

// withGitHubTransport swaps http.DefaultClient.Transport for a
// GitHub-rewriter for the duration of the test. Returns a cleanup the
// caller MUST defer (or register via t.Cleanup).
func withGitHubTransport(t *testing.T, f *fakeGitHubAPI) func() {
	t.Helper()
	origin, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatalf("parse impersonator URL: %v", err)
	}
	prev := http.DefaultClient.Transport
	if prev == nil {
		prev = http.DefaultTransport
	}
	http.DefaultClient.Transport = &rewriteTo{origin: origin, next: prev}
	return func() { http.DefaultClient.Transport = prev }
}

// defaultGitHubTestOAuthCfg is the issue #419 / ADR-046 pre-wired
// SignInConfig the GitHub callback tests assume. With
// GITHUB_CLIENT_ID + GITHUB_CLIENT_SECRET both Configured, the
// handler's boot-resolved-state guard is satisfied and the
// credential exchange runs as it did before #419. Tests that want
// the 503 path call newGitHubTestServer(t) instead, which leaves
// the oauthConfig at the zero (both-Disabled) value.
var defaultGitHubTestOAuthCfg = auth.SignInConfig{
	GitHub: auth.SignInProvider{
		Status:       auth.SignInProviderConfigured,
		ClientID:     "test_gh_client_id",
		ClientSecret: "test_gh_client_secret",
	},
}

func newGitHubTestServer(t *testing.T) http.Handler {
	t.Helper()
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newServer(store, log, "gregale.dev", noopNotifier{}).
		WithOAuthConfig(defaultGitHubTestOAuthCfg).handler()
}

// newGitHubTestServerDisabled is the issue #419 / ADR-046 helper for
// tests that need the disabled-provider 503 (both vars unset at
// boot). Use newGitHubTestServer for the Configured shape.
func newGitHubTestServerDisabled(t *testing.T) http.Handler {
	t.Helper()
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newServer(store, log, "gregale.dev", noopNotifier{}).handler()
}

// newGitHubTestServerWithOAuth is the variant that takes a custom
// SignInConfig — useful for tests that need a non-default config
// (e.g. an explicit RedirectURI override). Tests that need the
// standard Configured shape should call newGitHubTestServer; tests
// that need the disabled-provider 503 should call
// newGitHubTestServerDisabled.
func newGitHubTestServerWithOAuth(t *testing.T, cfg auth.SignInConfig) http.Handler {
	t.Helper()
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return newServer(store, log, "gregale.dev", noopNotifier{}).WithOAuthConfig(cfg).handler()
}

// --- 1. Consent redirect -------------------------------------------------

// TestGitHubAuthRedirect drives GET /v1/auth/github with the OAuth
// config wired as Configured (issue #419 / ADR-046: the handler now
// reads s.oauthConfig, not os.Getenv). Expects a 302 to
// github.com/login/oauth/authorize carrying the requested scope, plus
// a faas_github_state CSRF cookie scoped to /v1/auth/github/callback.
func TestGitHubAuthRedirect(t *testing.T) {
	h := newGitHubTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 Found, got %d; body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://github.com/login/oauth/authorize?") {
		t.Errorf("Location = %q, want GitHub consent URL", loc)
	}
	q, err := url.ParseQuery(loc[strings.Index(loc, "?")+1:])
	if err != nil {
		t.Fatalf("parse Location query: %v", err)
	}
	if got := q.Get("client_id"); got != "test_gh_client_id" {
		t.Errorf("client_id = %q, want test_gh_client_id", got)
	}
	if got := q.Get("scope"); got != "read:user user:email" {
		t.Errorf("scope = %q, want read:user user:email", got)
	}
	if q.Get("allow_signup") != "true" {
		t.Errorf("allow_signup = %q, want true", q.Get("allow_signup"))
	}
	if got := q.Get("code_challenge_method"); got != oauthpkce.MethodS256 {
		t.Errorf("code_challenge_method = %q, want %s", got, oauthpkce.MethodS256)
	}
	codeChallenge := q.Get("code_challenge")
	if codeChallenge == "" {
		t.Fatalf("expected non-empty PKCE code_challenge")
	}
	stateToken := q.Get("state")
	if stateToken == "" {
		t.Fatalf("expected non-empty state query param")
	}
	var seenState bool
	var verifier string
	for _, c := range rec.Result().Cookies() {
		if c.Name == githubAuthStateCookie {
			seenState = true
			if c.Value != stateToken {
				t.Errorf("state cookie = %q, want %q", c.Value, stateToken)
			}
			if c.Path != githubCallbackPath {
				t.Errorf("state cookie path = %q, want %q", c.Path, githubCallbackPath)
			}
		}
		if c.Name == githubAuthPKCECookie {
			verifier = c.Value
			if c.Path != githubCallbackPath {
				t.Errorf("PKCE cookie path = %q, want %q", c.Path, githubCallbackPath)
			}
		}
	}
	if !seenState {
		t.Errorf("expected faas_github_state CSRF cookie to be set")
	}
	if verifier == "" {
		t.Fatalf("expected faas_github_pkce verifier cookie to be set")
	}
	if !oauthpkce.Verify(verifier, codeChallenge) {
		t.Errorf("PKCE cookie verifier does not produce redirect challenge")
	}
}

// TestGitHubAuthRedirect_BothEnvUnset asserts the consent-redirect
// path is fail-closed when both GITHUB_CLIENT_ID and
// GITHUB_CLIENT_SECRET are unset on this host (issue #419 /
// ADR-046). 503 oauth_provider_unavailable so operators see the
// misconfiguration in logs immediately rather than a misleading CSRF
// flow. Distinct from the legacy 500 github_oauth_misconfigured,
// which is reserved for the defence-in-depth case where a Configured
// provider somehow read an empty value at request time. The name
// reflects the post-#419 shape: "both unset" is what reaches the
// runtime — half-set (one env var set) refuses to boot, so the
// runtime never sees it.
func TestGitHubAuthRedirect_BothEnvUnset(t *testing.T) {
	h := newGitHubTestServerDisabled(t)

	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "oauth_provider_unavailable" {
		t.Errorf("code = %v, want oauth_provider_unavailable", p["code"])
	}
}

// --- 2. Callback CSRF / input validation --------------------------------

func TestGitHubAuthCallback_CSRFMismatch(t *testing.T) {
	h := newGitHubTestServer(t)
	// Cookie present (anchor) but query state doesn't match — that's
	// the csrf_mismatch branch. Without the cookie, the handler
	// short-circuits at invalid_state (covered by
	// TestGitHubAuthCallback_MissingStateCookie below).
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?state=invalid_state&code=test_code", nil)
	req.AddCookie(&http.Cookie{Name: githubAuthStateCookie, Value: "expected_state"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 CSRF mismatch, got %d; body=%s", rec.Code, rec.Body.String())
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "csrf_mismatch" {
		t.Errorf("code = %v, want csrf_mismatch", p["code"])
	}
}

// TestGitHubAuthCallback_MissingStateCookie sends a callback with a
// state query but no faas_github_state cookie at all. Must 400
// invalid_state — the cookie isn't optional because the entire flow
// exists to anchor the state cookie to the browser.
func TestGitHubAuthCallback_MissingStateCookie(t *testing.T) {
	h := newGitHubTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?state=any&code=any", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "invalid_state" {
		t.Errorf("code = %v, want invalid_state", p["code"])
	}
}

func TestGitHubAuthCallback_MissingCode(t *testing.T) {
	h := newGitHubTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?state=any", nil)
	// Cookie + matching query state is the precondition for the
	// handler to reach the code-presence check. Without these, the
	// handler short-circuits at invalid_state (covered above).
	req.AddCookie(&http.Cookie{Name: githubAuthStateCookie, Value: "any"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 missing_code, got %d", rec.Code)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "missing_code" {
		t.Errorf("code = %v, want missing_code", p["code"])
	}
}

// TestGitHubAuthCallback_OAuthMisconfigured asserts the callback is
// fail-closed when GITHUB_CLIENT_SECRET is unset (even if the
// consent-redirect path is configured). Issue #419 / ADR-046:
// half-set config refuses to boot in production; at request time
// the disabled-provider guard fires first and the callback returns
// 503 oauth_provider_unavailable, not the legacy 500
// github_oauth_misconfigured (the legacy 500 is the defence-in-depth
// path that never fires under normal boot).
func TestGitHubAuthCallback_OAuthMisconfigured(t *testing.T) {
	cfg := auth.SignInConfig{
		GitHub: auth.SignInProvider{
			Status:   auth.SignInProviderConfigured,
			ClientID: "test_gh_client_id",
			// ClientSecret intentionally empty — the handler's
			// guard runs at the .Enabled() level, so a half-set
			// Configured provider reads as Disabled and returns
			// 503 oauth_provider_unavailable rather than 500.
			ClientSecret: "",
		},
	}
	h := newGitHubTestServerWithOAuth(t, cfg)
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?state=any&code=any", nil)
	req.AddCookie(&http.Cookie{Name: githubAuthStateCookie, Value: "any"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "oauth_provider_unavailable" {
		t.Errorf("code = %v, want oauth_provider_unavailable", p["code"])
	}
}

func TestGitHubAuthCallbackRequiresPKCECookie(t *testing.T) {
	h := newGitHubTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/github/callback?state=any&code=any", nil)
	req.AddCookie(&http.Cookie{Name: githubAuthStateCookie, Value: "any"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "invalid_pkce" {
		t.Errorf("code = %v, want invalid_pkce", p["code"])
	}
}

// --- 3. End-to-end mock-GitHub cases ------------------------------------

// newSignedGitHubRequest builds a callback request with a state cookie
// that matches the query — the CSRF precondition for every callback
// test that exercises the actual code-exchange path.
func newSignedGitHubRequest(queryState, code string) *http.Request {
	r := httptest.NewRequest(http.MethodGet,
		"/v1/auth/github/callback?state="+url.QueryEscape(queryState)+"&code="+url.QueryEscape(code), nil)
	r.AddCookie(&http.Cookie{Name: githubAuthStateCookie, Value: queryState})
	r.AddCookie(&http.Cookie{Name: githubAuthPKCECookie, Value: strings.Repeat("a", 43)})
	return r
}

// TestGitHubAuthCallback_UnverifiedEmail exercises the §11 invariant:
// a GitHub profile whose only email is `primary:true, verified:false`
// must return 401 email_not_verified and never mint a session. The
// session cookie must NOT be set on the response.
func TestGitHubAuthCallback_UnverifiedEmail(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test_gh_client_id")
	t.Setenv("GITHUB_CLIENT_SECRET", "test_gh_client_secret")
	f := newFakeGitHubAPI(t)
	f.userBody = []byte(`{"id":42,"login":"alice","name":"Alice","avatar_url":"https://x","email":""}`)
	f.emailsBody = []byte(`[{"email":"a@e.com","primary":true,"verified":false}]`)
	restore := withGitHubTransport(t, f)
	defer restore()

	h := newGitHubTestServer(t)
	req := newSignedGitHubRequest("abc", "code")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 unverified, got %d; body=%s", rec.Code, rec.Body.String())
	}
	var p map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if p["code"] != "email_not_verified" {
		t.Errorf("code = %v, want email_not_verified", p["code"])
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			t.Errorf("session cookie must NOT be set on unverified-email path: %v", c)
		}
	}
	// No oauth_links row was created for the impersonated sub=42.
	// The handler returns 401 BEFORE provisionOrFetchOAuthAccount
	// runs, so the (provider, sub) row must not exist. Asserting
	// against the handler's own store requires we share it; the
	// helper above (newGitHubTestServer) constructs a fresh store
	// per call, so we drive the same handler one more time with
	// the SAME store the test wired up. If a future refactor moves
	// the 401 emit to AFTER the provision step, this assertion will
	// fail loudly.
	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Same Configured OAuth config as the primary call (issue #419 /
	// ADR-046): the handler now refuses to do credential work unless
	// the boot-resolved SignInConfig is Configured. Without this the
	// second call would 503 oauth_provider_unavailable before the
	// 401 path runs, and the anti-takeover assertion would never
	// execute.
	srv := newServer(store, log, "gregale.dev", noopNotifier{}).
		WithOAuthConfig(defaultGitHubTestOAuthCfg).handler()
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("second unverified call: code = %d, want 401", rec2.Code)
	}
	if link, err := store.OAuthLinkByProviderSubject(context.Background(), "github", "42"); err == nil && link.AccountID != "" {
		t.Errorf("oauth_links row was created for sub=42 on the rejected path: %+v", link)
	}
}

// TestGitHubAuthCallback_FreshAccount drives the happy path end-to-end:
// GitHub impersonator returns a verified email + sub; the handler must
// create a Free-plan account on first sight, write the oauth_links
// row, mint a session cookie, and 302 to WEBSITE_URL.
func TestGitHubAuthCallback_FreshAccount(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test_gh_client_id")
	t.Setenv("GITHUB_CLIENT_SECRET", "test_gh_client_secret")
	t.Setenv("WEBSITE_URL", "https://dashboard.example.com/welcome")
	f := newFakeGitHubAPI(t)
	const ghID int64 = 4242
	f.userBody = []byte(`{"id":` + itoa(int(ghID)) + `,"login":"alice","name":"Alice","avatar_url":"https://x","email":""}`)
	f.emailsBody = []byte(`[{"email":"alice@example.com","primary":true,"verified":true}]`)
	restore := withGitHubTransport(t, f)
	defer restore()

	store := state.NewMemStore()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newServer(store, log, "gregale.dev", noopNotifier{}).
		WithOAuthConfig(defaultGitHubTestOAuthCfg).handler()

	req := newSignedGitHubRequest("st", "cd")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "https://dashboard.example.com/welcome" {
		t.Errorf("Location = %q, want WEBSITE_URL", loc)
	}
	var sessionSeen bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			sessionSeen = true
		}
	}
	if !sessionSeen {
		t.Fatalf("expected session cookie to be set on success")
	}
	link, err := store.OAuthLinkByProviderSubject(context.Background(), "github", itoa(int(ghID)))
	if err != nil {
		t.Fatalf("OAuthLinkByProviderSubject: %v", err)
	}
	if link.AccountID == "" {
		t.Fatalf("no oauth_links row for github/%d", ghID)
	}
	acct, err := store.AccountByID(context.Background(), link.AccountID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	if acct.Email != "alice@example.com" {
		t.Errorf("acct.Email = %q, want alice@example.com", acct.Email)
	}
	// Fresh accounts always land on the Free plan. A regression that
	// defaults a brand-new OAuth account to Pro (or any other paid
	// tier) would silently grant paid-tier quotas to unverified
	// prospects — the audit row would still emit but the customer
	// would be paying for nothing. api.PlanFree is the only allowed
	// default for the create-on-first-sight path (handlers_github.go:
	// 222 → provisionOrFetchOAuthAccount).
	if acct.Plan != api.PlanFree {
		t.Errorf("acct.Plan = %q, want %q (Free is the only valid default for fresh OAuth accounts)", acct.Plan, api.PlanFree)
	}
}

// TestGitHubAuthCallback_LegacyAccountBind covers the merge case: a
// pre-existing password account signs in with GitHub for the first
// time. The sub-first lookup misses; the email-based fallback binds
// the oauth_links row to the existing account.
func TestGitHubAuthCallback_LegacyAccountBind(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test_gh_client_id")
	t.Setenv("GITHUB_CLIENT_SECRET", "test_gh_client_secret")
	f := newFakeGitHubAPI(t)
	const ghID int64 = 9999
	f.userBody = []byte(`{"id":` + itoa(int(ghID)) + `,"login":"alice","name":"Alice","avatar_url":"https://x","email":""}`)
	f.emailsBody = []byte(`[{"email":"alice@example.com","primary":true,"verified":true}]`)
	restore := withGitHubTransport(t, f)
	defer restore()

	store := state.NewMemStore()
	preExisting, err := store.CreateAccount(context.Background(), "alice@example.com", "free")
	if err != nil {
		t.Fatalf("seed pre-existing: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newServer(store, log, "gregale.dev", noopNotifier{}).
		WithOAuthConfig(defaultGitHubTestOAuthCfg).handler()

	req := newSignedGitHubRequest("st2", "cd2")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body=%s", rec.Code, rec.Body.String())
	}
	link, err := store.OAuthLinkByProviderSubject(context.Background(), "github", itoa(int(ghID)))
	if err != nil {
		t.Fatalf("OAuthLinkByProviderSubject: %v", err)
	}
	if link.AccountID == "" {
		t.Fatalf("expected oauth_links row after bind")
	}
	if link.AccountID != preExisting.ID {
		t.Errorf("link AccountID = %s, want pre-existing %s", link.AccountID, preExisting.ID)
	}
}

// TestGitHubAuthCallback_SubFirstAntiTakeover is the §11 property
// regression. It fires TWO callbacks that share the same GitHub
// `sub` (provider_subject) but resolve to different verified
// emails — the second callback hits a pre-existing password account
// at its email. The (provider=github, sub) row MUST stay anchored
// to the first login's account; the second account MUST NOT pick
// up the foreign sub. MemStore's UpsertOAuthLink returns an error
// on a different-account re-bind which the handler currently
// swallows via s.log.Error, so the first row is preserved.
//
// Why two callbacks matter: a single login + an anchor check is
// tautological — the row cannot move between bind and assertion.
// The §11 invariant is exercised by the second callback's attempt
// to claim the sub for preExisting via the email-fallback path.
func TestGitHubAuthCallback_SubFirstAntiTakeover(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test_gh_client_id")
	t.Setenv("GITHUB_CLIENT_SECRET", "test_gh_client_secret")
	const sharedGHID int64 = 1234

	store := state.NewMemStore()
	preExisting, err := store.CreateAccount(context.Background(), "other@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("seed preExisting: %v", err)
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := newServer(store, log, "gregale.dev", noopNotifier{}).
		WithOAuthConfig(defaultGitHubTestOAuthCfg).handler()

	// --- First login: (sub, email=shared@example.com) lands on a
	// fresh Free account.
	f1 := newFakeGitHubAPI(t)
	f1.userBody = []byte(`{"id":` + itoa(int(sharedGHID)) + `,"login":"alice","name":"Alice","avatar_url":"https://x","email":""}`)
	f1.emailsBody = []byte(`[{"email":"shared@example.com","primary":true,"verified":true}]`)
	restore1 := withGitHubTransport(t, f1)
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, newSignedGitHubRequest("k1", "c1"))
	restore1()
	if rec1.Code != http.StatusFound {
		t.Fatalf("first login: code = %d, body=%s", rec1.Code, rec1.Body.String())
	}
	firstLink, err := store.OAuthLinkByProviderSubject(context.Background(), "github", itoa(int(sharedGHID)))
	if err != nil || firstLink.AccountID == "" {
		t.Fatalf("first OAuthLinkByProviderSubject: link=%v err=%v", firstLink, err)
	}
	firstAcctID := firstLink.AccountID
	if firstAcctID == preExisting.ID {
		t.Fatalf("first login should NOT bind to preExisting; got %s", firstAcctID)
	}

	// --- Second login: SAME GitHub sub, DIFFERENT verified email
	// (other@example.com). The email-fallback path hits preExisting,
	// which tries to claim the sub for itself.
	f2 := newFakeGitHubAPI(t)
	f2.userBody = []byte(`{"id":` + itoa(int(sharedGHID)) + `,"login":"alice","name":"Alice","avatar_url":"https://x","email":""}`)
	f2.emailsBody = []byte(`[{"email":"other@example.com","primary":true,"verified":true}]`)
	restore2 := withGitHubTransport(t, f2)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, newSignedGitHubRequest("k2", "c2"))
	restore2()

	// The (provider=github, sub=sharedGHID) row MUST still point at
	// firstAcctID. If the takeover succeeded, the row now points at
	// preExisting.ID and the §11 anti-takeover invariant is broken.
	link, err := store.OAuthLinkByProviderSubject(context.Background(), "github", itoa(int(sharedGHID)))
	if err != nil {
		t.Fatalf("OAuthLinkByProviderSubject after second login: %v", err)
	}
	if link.AccountID != firstAcctID {
		t.Errorf("§11 anti-takeover violated: sub %s now points to %s instead of firstAcctID (%s); a different-account UpsertOAuthLink succeeded when it should have been rejected",
			itoa(int(sharedGHID)), link.AccountID, firstAcctID)
	}
	// preExisting has NO github oauth_links row for sharedGHID. The
	// only path through the handler that would create one is the
	// successful UpsertOAuthLink at the email-fallback step; that
	// step must have failed (MemStore ErrConflict) for the invariant
	// to hold. The link is already anchored above; this redundant
	// check makes the failure mode more readable if the anchor ever
	// moves: "the row points at preExisting" tells the next reader
	// exactly which row was forged.
	if link.AccountID == preExisting.ID {
		t.Errorf("§11 anti-takeover: sharedGHID row IS bound to preExisting (%s) — the foreign-account re-bind was NOT rejected at the store layer; firstAcctID=%s",
			preExisting.ID, firstAcctID)
	}
}

// TestGitHubAuthCallback_EmitsAuditLoginEvent is the regression that
// closes PR-A: a successful GitHub OAuth flow must land an events row
// with kind=auth.login, data.method=github, data.email=<the verified
// email>, and data.login=<the GitHub username>. Mirrors the Google
// handler's audit emission at handlers_google.go:228-231.
//
// In addition to the audit-row shape, this test asserts the cross-
// system correlation invariant: the slog "github oauth sign-in
// successful" line and the audit row's data.login MUST carry the
// SAME value. Operators correlating slog and audit by login should
// see one identifier; a regression that hard-codes one but not the
// other (e.g., audit carries the email while slog carries the login)
// would silently break dashboard filtering.
func TestGitHubAuthCallback_EmitsAuditLoginEvent(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test_gh_client_id")
	t.Setenv("GITHUB_CLIENT_SECRET", "test_gh_client_secret")
	const ghID int64 = 7777
	f := newFakeGitHubAPI(t)
	f.userBody = []byte(`{"id":` + itoa(int(ghID)) + `,"login":"login-name","name":"Login Name","avatar_url":"https://x","email":""}`)
	f.emailsBody = []byte(`[{"email":"audit-gh@example.com","primary":true,"verified":true}]`)
	restore := withGitHubTransport(t, f)
	defer restore()

	store := state.NewMemStore()
	slogBuf := &safeBuffer{}
	log := slog.New(slog.NewTextHandler(slogBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	h := newServer(store, log, "gregale.dev", noopNotifier{}).
		WithOAuthConfig(defaultGitHubTestOAuthCfg).handler()

	req := newSignedGitHubRequest("audit-state", "audit-code")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body=%s", rec.Code, rec.Body.String())
	}

	link, err := store.OAuthLinkByProviderSubject(context.Background(), "github", itoa(int(ghID)))
	if err != nil || link.AccountID == "" {
		t.Fatalf("OAuthLinkByProviderSubject: link=%v err=%v", link, err)
	}
	rows, err := store.ListEvents(context.Background(), link.AccountID, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var authLogin *state.Event
	for i := range rows {
		if rows[i].Kind == "auth.login" {
			authLogin = &rows[i]
			break
		}
	}
	authLogin = mustAuditEventGitHub(t, authLogin, fmt.Sprintf("no auth.login event row landed; rows=%+v", rows))
	if authLogin.Subject == nil || authLogin.Subject.String() != uuidStringOf(link.AccountID) {
		t.Errorf("Subject = %v, want %s", authLogin.Subject, link.AccountID)
	}
	if authLogin.Actor != "apid" {
		t.Errorf("Actor = %q, want apid", authLogin.Actor)
	}
	var data map[string]any
	if err := json.Unmarshal(authLogin.Data, &data); err != nil {
		t.Fatalf("auth.login Data not JSON: %v", err)
	}
	if data["method"] != "github" {
		t.Errorf("auth.login Data.method = %v, want github", data["method"])
	}
	if data["email"] != "audit-gh@example.com" {
		t.Errorf("auth.login Data.email = %v, want audit-gh@example.com", data["email"])
	}
	if data["login"] != "login-name" {
		t.Errorf("auth.login Data.login = %v, want login-name", data["login"])
	}

	// Cross-system correlation: the slog "github oauth sign-in
	// successful" line carries `login=<slog login>` and the audit
	// row carries `data.login`. Both must equal "login-name" and
	// they must come from the SAME source (the GitHub user profile
	// the handler just fetched). We parse slog with key=value
	// pairs — sufficient for the slog.NewTextHandler output the
	// apid server uses.
	slogLine := slogBuf.String()
	if !strings.Contains(slogLine, "github oauth sign-in successful") {
		t.Fatalf("slog did not record the sign-in line; buf=%q", slogLine)
	}
	if !strings.Contains(slogLine, "login=login-name") {
		t.Errorf("slog sign-in line missing login=login-name; buf=%q", slogLine)
	}
	// Stronger check: extract the slog login value and assert it
	// equals data["login"]. This catches regressions that hard-code
	// different identifiers in the two paths (e.g., audit emits the
	// email while slog emits the login — they would BOTH pass the
	// individual contains checks above but FAIL this equality).
	slogLogin := slogFieldValue(t, slogLine, "login")
	if slogLogin != data["login"] {
		t.Errorf("slog login=%q ≠ audit data.login=%q (cross-system correlation broken)", slogLogin, data["login"])
	}
}

// slogFieldValue extracts a single key=value pair from a slog text
// handler output line. Returns "" if not found. Sufficient for the
// audit-correlation assertion; not a general slog parser.
func slogFieldValue(t *testing.T, line, key string) string {
	t.Helper()
	needle := key + "="
	for _, field := range strings.Fields(line) {
		if strings.HasPrefix(field, needle) {
			return strings.TrimSuffix(strings.TrimPrefix(field, needle), "\n")
		}
	}
	_ = needle // satisfy unused-var linter if needle isn't read (always is)
	return ""
}

// safeBuffer is an io.Writer guarded by a mutex. slog's text handler
// writes from the handler goroutine while the test reads the buffer
// from the test goroutine; a bare *bytes.Buffer races under -race.
// Per MEMORY.md/capturestdout-race, this is the standard pattern.
type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// --- 4. parseGitHubAccessToken unit tests -------------------------------

func TestParseGitHubAccessToken_JSON(t *testing.T) {
	body := []byte(`{"access_token":"abc","token_type":"bearer","scope":"repo"}`)
	if got := parseGitHubAccessToken(body); got != "abc" {
		t.Errorf("parseGitHubAccessToken(JSON) = %q, want abc", got)
	}
}

func TestParseGitHubAccessToken_FormEncoded(t *testing.T) {
	body := []byte(`access_token=xyz&scope=read%3Auser&token_type=bearer`)
	if got := parseGitHubAccessToken(body); got != "xyz" {
		t.Errorf("parseGitHubAccessToken(form) = %q, want xyz", got)
	}
}

// --- 5. Accept: application/json header regression ---------------------

// TestGitHubAuthCallback_TokenExchangeSendsAcceptJSON is the wire-shape
// regression: the access_token POST must carry Accept: application/json
// (per GitHub's OAuth-App docs) so the response is reliably JSON and
// not form-encoded. Before this fix the handler used http.PostForm
// which sets no Accept header.
func TestGitHubAuthCallback_TokenExchangeSendsAcceptJSON(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "test_gh_client_id")
	t.Setenv("GITHUB_CLIENT_SECRET", "test_gh_client_secret")
	f := newFakeGitHubAPI(t)
	const ghID int64 = 5555
	f.userBody = []byte(`{"id":` + itoa(int(ghID)) + `,"login":"alice","name":"Alice","avatar_url":"https://x","email":""}`)
	f.emailsBody = []byte(`[{"email":"hdr@example.com","primary":true,"verified":true}]`)
	restore := withGitHubTransport(t, f)
	defer restore()

	h := newGitHubTestServer(t)
	req := newSignedGitHubRequest("hd", "hr")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := f.acceptSeen.Load(); got < 1 {
		t.Errorf("access_token request did not carry Accept: application/json; saw %d", got)
	}
	if got := f.verifierSeen.Load(); got < 1 {
		t.Errorf("access_token request did not carry code_verifier; saw %d", got)
	}
}

// mustAuditEventGitHub mirrors mustAuditEvent (declared in
// handlers_audit_test.go) for the auth.login audit-row lookup.
// Go forbids duplicate function names in the same package even
// across _test.go files in package main, so we use a file-scoped
// name. Same SA5011 false positive.
func mustAuditEventGitHub(t *testing.T, ev *state.Event, msg string) *state.Event {
	t.Helper()
	if ev == nil {
		t.Fatal(msg)
	}
	return ev
}
