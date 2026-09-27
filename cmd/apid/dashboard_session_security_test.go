package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/onebox-faas/faas/pkg/api"
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
