package main

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

var dashboardCSRFField = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func dashboardGet(h http.Handler, path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	h.ServeHTTP(rec, r)
	return rec
}

func responseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestDashboard_MFAPendingSessionIsConfinedToTheChallenge — the dashboard
// ignored mfa_pending, so MFA protected only /v1: a password or magic
// link alone opened every dashboard page, including the account export.
// A pending session now reaches only /dashboard/mfa until a code verifies.
func TestDashboard_MFAPendingSessionIsConfinedToTheChallenge(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	_, secret, _ := e.generateEnrolledAccount(t)
	pending := e.mfaIssueWithPending(t, true)

	for _, path := range []string{"/dashboard/", "/dashboard/apps", "/dashboard/account/export"} {
		rec := dashboardGet(e.h, path, pending)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != dashboardMFAPath+"?next="+url.QueryEscape(path) {
			t.Fatalf("mfa_pending GET %s = %d Location=%q, want 302 %s", path, rec.Code, rec.Header().Get("Location"), dashboardMFAPath)
		}
	}

	challenge := func() (string, *http.Cookie) {
		t.Helper()
		rec := dashboardGet(e.h, dashboardMFAPath, pending)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", dashboardMFAPath, rec.Code, rec.Body)
		}
		m := dashboardCSRFField.FindStringSubmatch(rec.Body.String())
		csrf := responseCookie(rec, dashboardMFACSRFCookie)
		if m == nil || csrf == nil {
			t.Fatalf("challenge page missing CSRF token or cookie:\n%s", rec.Body)
		}
		return m[1], csrf
	}
	post := func(code string) *httptest.ResponseRecorder {
		t.Helper()
		token, csrf := challenge()
		form := url.Values{"csrf_token": {token}, "code": {code}}
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, dashboardMFAPath, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(pending)
		r.AddCookie(csrf)
		e.h.ServeHTTP(rec, r)
		return rec
	}

	if rec := post("000000"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d, want 401 (counted by the auth limiter)", rec.Code)
	}
	code, err := totp.GenerateCodeCustom(secret, time.Now().UTC(), totp.ValidateOpts{Period: 30, Digits: 6})
	if err != nil {
		t.Fatal(err)
	}
	rec := post(code)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/" {
		t.Fatalf("right code = %d Location=%q, want 303 /dashboard/", rec.Code, rec.Header().Get("Location"))
	}
	cleared := responseCookie(rec, sessionCookie)
	if cleared == nil {
		t.Fatal("verify did not re-issue the session cookie")
	}
	if env, err := e.mgr.Verify(cleared.Value); err != nil || env.MfaPending {
		t.Fatalf("re-issued cookie: pending=%v err=%v, want cleared", env.MfaPending, err)
	}
	if rec := dashboardGet(e.h, "/dashboard/account/export", cleared); rec.Code != http.StatusOK {
		t.Fatalf("export after MFA = %d, want 200", rec.Code)
	}
}

// TestDashboard_RevokedSessionIsSignedOut — /v1 checks the live sessions
// row; the dashboard only checked the cookie signature, so a revoked
// session (sign-out everywhere, stolen-cookie revoke) and a sid-less
// legacy cookie kept every dashboard page, including the export.
func TestDashboard_RevokedSessionIsSignedOut(t *testing.T) {
	h, cookie, store, mgr := newAuthedDashboardServerFull(t)
	if rec := dashboardGet(h, "/dashboard/account/export", cookie); rec.Code != http.StatusOK {
		t.Fatalf("live session export = %d, want 200", rec.Code)
	}
	env, err := mgr.Verify(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeSession(t.Context(), env.Sid, env.AccountID); err != nil {
		t.Fatal(err)
	}
	legacy, err := mgr.Issue(env.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]*http.Cookie{"revoked": cookie, "sid-less": {Name: sessionCookie, Value: legacy}} {
		rec := dashboardGet(h, "/dashboard/account/export", c)
		if rec.Code != http.StatusFound || rec.Header().Get("Location") != loginPath {
			t.Errorf("%s session export = %d Location=%q, want 302 %s", name, rec.Code, rec.Header().Get("Location"), loginPath)
		}
	}
}

// TestAccountExport_RequiresCompletedMFA — the /v1 export twin had no
// requireMFA either: an mfa_pending cookie could download the account.
func TestAccountExport_RequiresCompletedMFA(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	e.generateEnrolledAccount(t)
	pending := e.mfaIssueWithPending(t, true)
	rec := dashboardGet(e.h, "/v1/account/export", pending)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
		t.Fatalf("mfa_pending export = %d %s, want 403 %s", rec.Code, rec.Body, api.CodeMFARequired)
	}
}

// TestDashboardMFA_PolicyWithoutEnrollmentPointsAtTheCLI — an account an
// explicit policy requires to use MFA has no code to enter yet.
func TestDashboardMFA_PolicyWithoutEnrollmentPointsAtTheCLI(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, true, false)
	rec := dashboardGet(e.h, dashboardMFAPath, e.mfaIssueWithPending(t, true))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "gregale mfa enroll") || strings.Contains(rec.Body.String(), `name="code"`) {
		t.Fatalf("GET %s for an unenrolled required account = %d:\n%s", dashboardMFAPath, rec.Code, rec.Body)
	}
}

// TestEventsStream_RejectsRevokedAndPendingSessions — /v1/events verified
// only the cookie signature, so a revoked or mfa_pending session could
// hold the account's live event stream open.
func TestEventsStream_RejectsRevokedAndPendingSessions(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	e.generateEnrolledAccount(t)
	if rec := dashboardGet(e.h, "/v1/events", e.mfaIssueWithPending(t, true)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("mfa_pending /v1/events = %d, want 401", rec.Code)
	}

	h, cookie, store, mgr := newAuthedDashboardServerFull(t)
	env, err := mgr.Verify(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RevokeSession(t.Context(), env.Sid, env.AccountID); err != nil {
		t.Fatal(err)
	}
	if rec := dashboardGet(h, "/v1/events", cookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked /v1/events = %d, want 401", rec.Code)
	}
}

// TestPasswordReset_RevokesEverySession — a reset is the recovery path for
// a compromised account, but it left every existing session alive, so an
// attacker signed in with the old password stayed signed in.
func TestPasswordReset_RevokesEverySession(t *testing.T) {
	h, cookie, store, mgr := newAuthedDashboardServerFull(t)
	env, err := mgr.Verify(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	if err := store.IssueLoginToken(t.Context(), api.HashToken(raw), env.AccountID, time.Now().Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"token": {base64.RawURLEncoding.EncodeToString(raw)}, "password": {chosenPassword}}
	req := httptest.NewRequest(http.MethodPost, "/auth/reset", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("reset = %d %s", rec.Code, rec.Body)
	}
	if got := dashboardGet(h, "/dashboard/account", cookie); got.Code != http.StatusFound || got.Header().Get("Location") != loginPath {
		t.Fatalf("pre-reset session after reset = %d Location=%q, want 302 %s", got.Code, got.Header().Get("Location"), loginPath)
	}
	fresh := responseCookie(rec, sessionCookie)
	if fresh == nil {
		t.Fatal("reset did not sign the customer in")
	}
	if got := dashboardGet(h, "/dashboard/account", fresh); got.Code != http.StatusOK {
		t.Fatalf("post-reset session = %d, want 200", got.Code)
	}
}

// TestSetPassword_ChangeSignsOutOtherSessions — changing a password kept
// every other session signed in.
func TestSetPassword_ChangeSignsOutOtherSessions(t *testing.T) {
	h, current, store, mgr := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)
	other := &http.Cookie{Name: sessionCookie, Value: issueDashboardTestCookie(t, store, mgr, id)}

	rec := postSetPasswordForm(t, h, current, mgr, id, url.Values{
		"password":         {chosenPassword},
		"current_password": {seededPassword},
	})
	if rec.Code != http.StatusFound {
		t.Fatalf("set-password = %d %s", rec.Code, rec.Body)
	}
	if got := dashboardGet(h, "/dashboard/account", other); got.Code != http.StatusFound || got.Header().Get("Location") != loginPath {
		t.Fatalf("other session after password change = %d Location=%q, want 302 %s", got.Code, got.Header().Get("Location"), loginPath)
	}
	if got := dashboardGet(h, "/dashboard/account", current); got.Code != http.StatusOK {
		t.Fatalf("the session that changed the password = %d, want 200", got.Code)
	}
}

// TestCLIAuthApproval_RequiresCompletedMFA — the CLI device-login approval
// page sits behind sessionAuth. While that ignored mfa_pending, a password
// alone could approve a CLI login and receive an API key, and API keys
// never face MFA: a permanent bypass.
func TestCLIAuthApproval_RequiresCompletedMFA(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	e.generateEnrolledAccount(t)
	pending := e.mfaIssueWithPending(t, true)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, cliAuthPath+"?code=ABCD-EFGH", strings.NewReader("code=ABCD-EFGH"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(pending)
		e.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), dashboardMFAPath) {
			t.Fatalf("mfa_pending %s %s = %d Location=%q, want 302 %s", method, cliAuthPath, rec.Code, rec.Header().Get("Location"), dashboardMFAPath)
		}
	}
}

func TestDashboardMFANext_OnlySameOriginDashboardPaths(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"":                             "/dashboard/",
		"/dashboard/apps?x=1":          "/dashboard/apps?x=1",
		cliAuthPath + "?code=AB-CD":    cliAuthPath + "?code=AB-CD",
		"/dashboard":                   "/dashboard",
		"//evil.example/dashboard/":    "/dashboard/",
		"https://evil.example/":        "/dashboard/",
		"/\\evil.example":              "/dashboard/",
		"/v1/account/export":           "/dashboard/",
		dashboardMFAPath + "?next=/x":  "/dashboard/",
		"/dashboard/%0d%0aSet-Cookie:": "/dashboard/%0d%0aSet-Cookie:",
		"dashboard/apps":               "/dashboard/",
	} {
		if got := dashboardMFANext(raw); got != want {
			t.Errorf("dashboardMFANext(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestOrgAPIKeys_RequireTheCallersScope — the org key routes checked the
// member's org role but not the calling key's scopes, so an org owner's
// apps:read key minted an admin key (POST /v1/keys refused the same key).
func TestOrgAPIKeys_RequireTheCallersScope(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanPro, "")
	call := func(key, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Active-Org", "scope-team")
		env.h.ServeHTTP(rec, r)
		return rec
	}
	if rec := call(env.key, "POST", "/v1/orgs", `{"slug":"scope-team","name":"Scope"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create org = %d %s", rec.Code, rec.Body)
	}
	readOnly, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateAPIKey(t.Context(), env.acct.ID, hash, "ci-read", []string{api.ScopeAppsRead}); err != nil {
		t.Fatal(err)
	}
	rec := call(readOnly, "POST", "/v1/orgs/scope-team/keys", `{"label":"escalated","scopes":["admin"]}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "insufficient_scope") {
		t.Fatalf("apps:read key minting an org admin key = %d %s, want 403 insufficient_scope", rec.Code, rec.Body)
	}
	if rec := call(env.key, "POST", "/v1/orgs/scope-team/keys", `{"label":"legit","scopes":["admin"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("admin key minting an org key = %d %s, want 201", rec.Code, rec.Body)
	}
	if rec := call(readOnly, "GET", "/v1/orgs/scope-team/keys", ""); rec.Code != http.StatusOK {
		t.Fatalf("apps:read key listing org keys = %d %s, want 200", rec.Code, rec.Body)
	}
}

func mfaDisableWithPassword(t *testing.T, e mfaTestEnv, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	csrfRec := dashboardGet(e.h, "/v1/auth/csrf?action=mfa_disable", session)
	var tok struct {
		Token string `json:"csrf_token"`
	}
	if err := json.Unmarshal(csrfRec.Body.Bytes(), &tok); err != nil || tok.Token == "" {
		t.Fatalf("csrf = %d %s", csrfRec.Code, csrfRec.Body)
	}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/disable",
		strings.NewReader(`{"password":"`+seededPassword+`","csrf_token":"`+tok.Token+`"}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(session)
	r.AddCookie(responseCookie(csrfRec, middleware.CookieNameAuthenticated))
	e.h.ServeHTTP(rec, r)
	return rec
}

// TestMFADisable_PasswordNeedsACompletedMFASession — /v1/account/mfa/disable
// is reachable by an mfa_pending session and accepted the password as
// proof. The password is the first factor: with only a stolen password an
// attacker signed in, switched MFA off, and could enroll their own device.
func TestMFADisable_PasswordNeedsACompletedMFASession(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	_, _, completed := e.generateEnrolledAccount(t)
	seedPassword(t, e.store, e.acct.ID)

	if rec := mfaDisableWithPassword(t, e, e.mfaIssueWithPending(t, true)); rec.Code != http.StatusForbidden {
		t.Fatalf("password disable from an mfa_pending session = %d %s, want 403", rec.Code, rec.Body)
	}
	if acct, _ := e.store.AccountByID(t.Context(), e.acct.ID); !acct.MFAEnrolled() {
		t.Fatal("an mfa_pending session disabled MFA with the password alone")
	}
	if rec := mfaDisableWithPassword(t, e, completed); rec.Code != http.StatusOK {
		t.Fatalf("password disable from a session that passed MFA = %d %s, want 200", rec.Code, rec.Body)
	}
}

// TestMFAVerify_GuessesAreLimitedPerAccount — the auth limiter counts
// failures per IP only, so a password holder with many addresses could
// keep guessing TOTP codes for one account. After ten wrong codes the
// account's TOTP checks answer 429, even for a new address, until the
// window moves on.
func TestMFAVerify_GuessesAreLimitedPerAccount(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	_, secret, _ := e.generateEnrolledAccount(t)
	pending := e.mfaIssueWithPending(t, true)
	verify := func(code, ip string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/verify", strings.NewReader(`{"totp":"`+code+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip + ":40000"
		r.AddCookie(pending)
		e.h.ServeHTTP(rec, r)
		return rec.Code
	}
	for i := 0; i < totpMaxFailures; i++ {
		if code := verify("000000", "198.51.100."+strconv.Itoa(i+1)); code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d from a fresh IP = %d, want 401", i+1, code)
		}
	}
	good, err := totp.GenerateCodeCustom(secret, time.Now().UTC(), totp.ValidateOpts{Period: 30, Digits: 6})
	if err != nil {
		t.Fatal(err)
	}
	if code := verify(good, "203.0.113.77"); code != http.StatusTooManyRequests {
		t.Fatalf("code after %d account failures from a new IP = %d, want 429", totpMaxFailures, code)
	}
}

func TestTOTPGuard_WindowAndReset(t *testing.T) {
	t.Parallel()
	g := newTOTPGuard()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	for i := 0; i < totpMaxFailures; i++ {
		g.fail("a", now.Add(time.Duration(i)*time.Second))
	}
	if wait := g.retryAfter("a", now.Add(10*time.Second)); wait <= 0 || wait > totpFailureWindow {
		t.Fatalf("retryAfter at the limit = %s", wait)
	}
	if wait := g.retryAfter("b", now); wait != 0 {
		t.Fatalf("another account is limited: %s", wait)
	}
	if wait := g.retryAfter("a", now.Add(totpFailureWindow+time.Second)); wait != 0 {
		t.Fatalf("retryAfter after the window = %s, want 0", wait)
	}
	g.fail("c", now)
	g.reset("c")
	if len(g.fails) != 0 && g.fails["c"] != nil {
		t.Fatal("reset left failures behind")
	}
}

// TestSessionWrites_RejectOtherOrigins — customer apps live on
// <slug>.gregale.dev, same-site with the API, so SameSite=Lax attaches the
// session cookie to a no-cors fetch from any customer's page. With only a
// visitor's cookie such a page deployed its own image into the visitor's
// app, parked it, and minted admin keys.
func TestSessionWrites_RejectOtherOrigins(t *testing.T) {
	h, cookie, store, _ := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "victim-app", Runtime: "node22", RAMMB: 128, Status: state.AppActive}); err != nil {
		t.Fatal(err)
	}
	send := func(origin, site string, withCookie bool, bearer string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "http://api.gregale.dev/v1/apps/victim-app/park", nil)
		r.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		if withCookie {
			r.AddCookie(cookie)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, c := range []struct {
		name, origin, site string
	}{
		{"sibling app, same-site", "https://evil.gregale.dev", "same-site"},
		{"sibling app, no fetch metadata", "https://evil.gregale.dev", ""},
		{"other site", "https://evil.example", "cross-site"},
	} {
		if code := send(c.origin, c.site, true, ""); code != http.StatusForbidden {
			t.Errorf("%s: cookie-authenticated park = %d, want 403", c.name, code)
		}
	}
	if code := send("http://api.gregale.dev", "same-origin", true, ""); code == http.StatusForbidden {
		t.Errorf("same-origin cookie request was refused")
	}
	if code := send("", "", true, ""); code == http.StatusForbidden {
		t.Errorf("non-browser cookie request (no Origin, no Sec-Fetch-Site) was refused")
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(t.Context(), acct.ID, hash, "ci", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	if code := send("https://evil.gregale.dev", "same-site", false, pt); code == http.StatusForbidden {
		t.Errorf("bearer request with a foreign Origin was refused; browsers never attach API keys")
	}
}

// TestCLIAuthApproval_ExpiredCodeNamesTheShippedCLI — the approval page
// told the customer to restart `faas login`; the CLI is gregale.
func TestCLIAuthApproval_ExpiredCodeNamesTheShippedCLI(t *testing.T) {
	h, cookie, store, mgr := newAuthedDashboardServerFull(t)
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	token, err := middleware.IssueForAuthenticated(mgr, "cli-auth", acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"code": {"ABCD2345"}, "csrf_token": {token}}
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, cliAuthPath, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(cookie)
	r.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: token})
	h.ServeHTTP(rec, r)
	body := rec.Body.String()
	if !strings.Contains(body, "gregale login") || strings.Contains(body, "faas login") {
		t.Fatalf("expired-code page = %d, want a gregale login hint:\n%s", rec.Code, body)
	}
}

// TestSignIn_RejectsOtherOrigins — login and signup accepted posts from any
// page. A customer's app on a sibling subdomain (same-site, so the session
// cookie it causes to be set is kept) could sign a visitor into the
// attacker's account.
func TestSignIn_RejectsOtherOrigins(t *testing.T) {
	h, _, store, _ := newAuthedDashboardServerFull(t)
	id := accountID(t, store, "alice@example.com")
	seedPassword(t, store, id)
	login := func(origin string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "http://api.gregale.dev/v1/auth/login",
			strings.NewReader(`{"email":"alice@example.com","password":"`+seededPassword+`"}`))
		r.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		if origin != "" {
			r.Header.Set("Origin", origin)
			r.Header.Set("Sec-Fetch-Site", "same-site")
		}
		h.ServeHTTP(rec, r)
		return rec
	}
	rec := login("https://evil.gregale.dev")
	if rec.Code != http.StatusForbidden || responseCookie(rec, sessionCookie) != nil {
		t.Fatalf("sibling-origin login = %d (cookie set: %v), want 403 and no session", rec.Code, responseCookie(rec, sessionCookie) != nil)
	}
	if rec := login(""); rec.Code == http.StatusForbidden {
		t.Fatalf("non-browser login was refused: %s", rec.Body)
	}
}

// TestOrgRemoval_EndsTheMembersAccess — removing a member stamped
// removed_at, but LoadOrg accepted the stamped row as a membership: the
// removed member kept their role, and the org admin key they had minted
// invited a new admin (them again) after removal.
func TestOrgRemoval_EndsTheMembersAccess(t *testing.T) {
	env, _ := setupChangePlan(t, api.PlanPro, "")
	call := func(key, method, path, body string) int {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Active-Org", "offboard-team")
		env.h.ServeHTTP(rec, r)
		return rec.Code
	}
	if code := call(env.key, "POST", "/v1/orgs", `{"slug":"offboard-team","name":"Team"}`); code != http.StatusCreated {
		t.Fatalf("create org = %d", code)
	}
	org, err := env.store.OrgBySlug(t.Context(), "offboard-team")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := env.store.CreateAccount(t.Context(), "bob@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.AddOrgMember(t.Context(), org.ID, bob.ID, state.OrgRoleAdmin, &env.acct.ID); err != nil {
		t.Fatal(err)
	}
	bobKey, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateOrgAPIKeyWithProvenance(t.Context(), org.ID, bob.ID, hash, "bob", []string{api.ScopeAdmin}, nil, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if code := call(bobKey, "GET", "/v1/orgs/offboard-team/members", ""); code != http.StatusOK {
		t.Fatalf("member listing members = %d, want 200", code)
	}
	if err := env.store.RemoveOrgMember(t.Context(), org.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/v1/orgs/offboard-team/members", ""},
		{"GET", "/v1/orgs/offboard-team/keys", ""},
		{"POST", "/v1/orgs/offboard-team/members", `{"email":"bob-again@example.com","role":"admin"}`},
	} {
		if code := call(bobKey, c.method, c.path, c.body); code != http.StatusForbidden {
			t.Errorf("removed member %s %s = %d, want 403", c.method, c.path, code)
		}
	}
}
