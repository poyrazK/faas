package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// passwordlessAccount is a verified account that signs in only with Google
// and GitHub: no password, no authenticator. Its browser session is the
// only proof it can give.
func passwordlessAccount(t *testing.T) (*apidserver.Server, http.Handler, *apidserver.RecordingMailer, state.Store, *session.Manager, state.Account, *http.Cookie) {
	t.Helper()
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	acct, _ := flowAccountKey(t, store, "passwordless@example.com", true)
	for _, p := range []string{"google", "github"} {
		if err := store.UpsertOAuthLink(t.Context(), acct.ID, p, p+"-sub", acct.Email, true); err != nil {
			t.Fatal(err)
		}
	}
	mgr := *srv.Sessions()
	return srv, srv.Handler(), mailer, store, mgr, acct, flowSession(t, store, mgr, acct.ID)
}

// setPasswordForm posts the account page's set-password form.
func setPasswordForm(t *testing.T, h http.Handler, mgr *session.Manager, cookie *http.Cookie, accountID string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := middleware.IssueForAuthenticated(mgr, "set_password", accountID)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"password": {"a-brand-new-horse-battery"}, "csrf_token": {tok}}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/account/set-password", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(cookie)
	req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A browser session that signed in more than RecentSignInWindow ago cannot
// set a first password, change the email, remove a sign-in method or start
// two-factor setup on an account it is the only proof for: a copied cookie
// signs in again first (ADR 20261110100000).
func TestStaleSessionCannotTakeOverPasswordlessAccount(t *testing.T) {
	_, h, _, store, mgr, acct, cookie := passwordlessAccount(t)
	apidserver.AgeSignInsForTest(t, api.RecentSignInWindow+time.Minute)

	checks := map[string]*httptest.ResponseRecorder{
		"email change": flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email",
			"account_email_change", map[string]any{"email": "attacker@example.net"}),
		"unlink":       flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodDelete, "/v1/account/sign-in-methods/github", "sign_in_method_unlink", nil),
		"mfa enroll":   flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/mfa/enroll", "", nil),
		"set password": setPasswordForm(t, h, mgr, cookie, acct.ID),
	}
	for name, rec := range checks {
		if rec.Code != http.StatusForbidden || problemCode(t, rec.Body.Bytes()) != api.CodeRecentSignInRequired {
			t.Errorf("%s from a stale session = %d %s, want 403 %s", name, rec.Code, rec.Body.String(), api.CodeRecentSignInRequired)
		}
	}

	tok, err := middleware.IssueForAuthenticatedNamed(mgr, "account_provider_unlink", acct.ID, "faas_csrf_account_provider_unlink")
	if err != nil {
		t.Fatal(err)
	}
	rec := flowForm(h, "/dashboard/account/providers/github/unlink", url.Values{"csrf_token": {tok}}, cookie,
		&http.Cookie{Name: "faas_csrf_account_provider_unlink", Value: tok})
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != "/login?reauth=1&next=/dashboard/account" {
		t.Errorf("account page unlink from a stale session = %d %q, want the sign-in page", rec.Code, loc)
	}

	methods, err := store.(state.AccountFlowStore).ListOAuthLinks(t.Context(), acct.ID)
	if err != nil || len(methods) != 2 {
		t.Fatalf("a refused unlink removed a provider: %d links (err %v)", len(methods), err)
	}
	if _, err := store.AccountPasswordByAccountID(t.Context(), acct.ID); err == nil {
		t.Fatal("a stale session set the first password")
	}
}

// The same session right after signing in goes through.
func TestRecentSessionChangesPasswordlessAccount(t *testing.T) {
	srv, h, mailer, _, mgr, acct, cookie := passwordlessAccount(t)
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/email",
		"account_email_change", map[string]any{"email": "owner-new@example.com"}); rec.Code != http.StatusAccepted {
		t.Errorf("email change from a recent session = %d %s, want 202", rec.Code, rec.Body.String())
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodDelete, "/v1/account/sign-in-methods/github", "sign_in_method_unlink", nil); rec.Code != http.StatusNoContent {
		t.Errorf("unlink from a recent session = %d %s, want 204", rec.Code, rec.Body.String())
	}
	if rec := flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/account/mfa/enroll", "", nil); problemCode(t, rec.Body.Bytes()) == api.CodeRecentSignInRequired {
		t.Errorf("mfa enroll from a recent session refused: %s", rec.Body.String())
	}
	if rec := setPasswordForm(t, h, mgr, cookie, acct.ID); rec.Code != http.StatusFound {
		t.Errorf("set password from a recent session = %d %s, want 302", rec.Code, rec.Body.String())
	}

	// The current address hears about the email change while it can stop
	// it, and about the removed provider.
	srv.WaitPasswordResetMail()
	var requested, removed bool
	for _, m := range mailer.Snapshot() {
		if m.To[0] != acct.Email {
			continue
		}
		requested = requested || strings.Contains(m.Subject, "asked to change your Gregale sign-in email")
		removed = removed || strings.Contains(m.Subject, "GitHub sign-in removed")
	}
	if !requested || !removed {
		t.Errorf("notices to the current address: change requested %v, provider removed %v", requested, removed)
	}
}
