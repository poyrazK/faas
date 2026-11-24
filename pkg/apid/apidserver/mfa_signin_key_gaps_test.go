// adr: 20261111110000
package apidserver_test

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// pendingCliCode issues a pending 8-character CLI login code.
func pendingCliCode(t *testing.T, store state.Store, raw string) {
	t.Helper()
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.IssueCliAuthCode(t.Context(), api.HashToken(decoded), time.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

// approveCliCode posts the approval form for raw on cookie's session.
func approveCliCode(t *testing.T, srv *apidserver.Server, cookie *http.Cookie, accountID, raw string) *httptest.ResponseRecorder {
	t.Helper()
	tok, err := middleware.IssueForAuthenticated(*srv.Sessions(), "cli-auth", accountID)
	if err != nil {
		t.Fatal(err)
	}
	return flowForm(srv.Handler(), "/cli-auth", url.Values{"code": {raw}, "csrf_token": {tok}}, cookie,
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
}

// Approving a CLI sign-in mints an admin key, so it needs what minting one
// directly needs: a verified email and a fresh sign-in.
func TestCliApprovalNeedsVerifiedEmailAndRecentSignIn(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	mgr := *srv.Sessions()

	unverified, _ := flowAccountKey(t, store, "cli-unverified@example.com", false)
	cookie := flowSession(t, store, mgr, unverified.ID)
	pendingCliCode(t, store, "a1b2c3d4")
	recovery := approveCliCode(t, srv, cookie, unverified.ID, "a1b2c3d4")
	body := recovery.Body.String()
	if !strings.Contains(body, "Verify your email first") || !strings.Contains(body, "Email verified") ||
		!strings.Contains(body, "<code>gregale login</code>") || strings.Contains(body, "already used or has expired") {
		t.Fatalf("unverified approval = %d %q, want the email verification steps", recovery.Code, body)
	}
	checkVerificationResendForm(t, srv.Handler(), recovery, cookie, mailer)

	owner, _ := flowAccountKey(t, store, "cli-owner@example.com", true)
	session := flowSession(t, store, mgr, owner.ID)
	apidserver.AgeSignInsForTest(t, api.RecentSignInWindow+time.Minute)
	rec := approveCliCode(t, srv, session, owner.ID, "a1b2c3d4")
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/login?reauth=1&next=") || !strings.Contains(loc, url.QueryEscape("/cli-auth?code=A1B2C3D4")) {
		t.Errorf("stale-session approval = %d %q, want sign-in that returns to the code", rec.Code, loc)
	}
	if _, approver, err := store.PeekCliAuthCode(t.Context(), api.HashToken(mustHex(t, "a1b2c3d4"))); err != nil || approver != "" {
		t.Fatalf("a refused approval claimed the code for %q (%v)", approver, err)
	}
}

func checkVerificationResendForm(t *testing.T, handler http.Handler, recovery *httptest.ResponseRecorder, cookie *http.Cookie, mailer *apidserver.RecordingMailer) {
	t.Helper()
	const path = "/v1/auth/verify-email/resend"
	token := flowFormToken(t, recovery.Body.String(), path)
	post := func(csrfCookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(url.Values{"csrf_token": {token}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "text/html")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.AddCookie(cookie)
		if csrfCookie != nil {
			req.AddCookie(csrfCookie)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(nil); rec.Code != http.StatusForbidden || len(mailer.Snapshot()) != 0 {
		t.Fatalf("resend without the form cookie = %d, want 403 and no mail", rec.Code)
	}
	for _, c := range recovery.Result().Cookies() {
		if c.Value != token {
			continue
		}
		rec := post(c)
		messages := mailer.Snapshot()
		if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "Check your email") ||
			len(messages) != 1 || len(messages[0].To) != 1 || messages[0].To[0] != "cli-unverified@example.com" {
			t.Fatalf("CLI verification resend = %d, mails = %d: %s", rec.Code, len(messages), rec.Body.String())
		}
		return
	}
	t.Fatal("verification recovery page did not set its form cookie")
}

func TestCliApprovalFromRecentSignInClaimsTheCode(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	owner, _ := flowAccountKey(t, store, "cli-recent@example.com", true)
	pendingCliCode(t, store, "0badcafe")
	rec := approveCliCode(t, srv, flowSession(t, store, *srv.Sessions(), owner.ID), owner.ID, "0badcafe")
	if _, approver, err := store.PeekCliAuthCode(t.Context(), api.HashToken(mustHex(t, "0badcafe"))); err != nil || approver != owner.ID {
		t.Fatalf("approval from a recent sign-in = %d %q, code approved by %q (%v)", rec.Code, rec.Header().Get("Location"), approver, err)
	}
}

// An account with two-factor authentication is fresh only after a code. A
// remembered browser signs in without one, so sending it to sign in again
// looped; it goes to the authenticator challenge and back to the code.
func TestCliApprovalWithMFASendsToChallengeNotSignIn(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	owner, _ := flowAccountKey(t, store, "cli-mfa@example.com", true)
	if err := store.SetMFASecret(t.Context(), owner.ID, []byte("secret"), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkMFAEnrolled(t.Context(), owner.ID); err != nil {
		t.Fatal(err)
	}
	pendingCliCode(t, store, "c0ffee11")
	// A just-issued session with no step-up: what a remembered browser's
	// sign-in mints.
	rec := approveCliCode(t, srv, flowSession(t, store, *srv.Sessions(), owner.ID), owner.ID, "c0ffee11")
	want := "/dashboard/mfa?next=" + url.QueryEscape("/cli-auth?code=C0FFEE11")
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || loc != want {
		t.Fatalf("approval without a step-up = %d %q, want 303 %q", rec.Code, loc, want)
	}
	if _, approver, err := store.PeekCliAuthCode(t.Context(), api.HashToken(mustHex(t, "c0ffee11"))); err != nil || approver != "" {
		t.Fatalf("approval without a step-up claimed the code for %q (%v)", approver, err)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Turning on two-factor authentication ends the other browsers and the CLI
// and MCP sign-in keys that never passed the new factor; hand-made keys stay.
func TestMFAEnrollEndsOtherSessionsAndSignInKeys(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	acct, _ := flowAccountKey(t, store, "enrolling@example.com", true)
	other := flowSession(t, store, *srv.Sessions(), acct.ID)
	_, cliHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	cliKey, err := store.CreateAPIKey(t.Context(), acct.ID, cliHash, "cli-login", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	_, handHash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	handKey, err := store.CreateAPIKey(t.Context(), acct.ID, handHash, "ci", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}

	srv.EndOtherAccessAfterMFAEnroll(httptest.NewRequest(http.MethodPost, "/v1/account/mfa/confirm", nil), acct)

	page := httptest.NewRequest(http.MethodGet, "/dashboard/account", nil)
	page.AddCookie(other)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, page)
	if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("another browser after MFA was turned on = %d %q, want sent to sign in", rec.Code, rec.Header().Get("Location"))
	}
	if k, err := store.GetAPIKey(t.Context(), acct.ID, cliKey.ID); err != nil || k.Status != "revoked" {
		t.Errorf("CLI sign-in key after MFA enrollment = %+v %v, want revoked", k, err)
	}
	if k, err := store.GetAPIKey(t.Context(), acct.ID, handKey.ID); err != nil || k.Status == "revoked" {
		t.Errorf("hand-made key after MFA enrollment = %+v %v, want kept", k, err)
	}
}

// Deleting the account or handing an organization away from a browser on an
// account without two-factor authentication needs a recent sign-in.
func TestIrreversibleChangesNeedRecentSignInWithoutMFA(t *testing.T) {
	srv, _, store := apidserver.V1AuthTestHarness(t)
	h, mgr := srv.Handler(), *srv.Sessions()
	acct, _ := flowAccountKey(t, store, "irreversible@example.com", true)
	cookie := flowSession(t, store, mgr, acct.ID)
	org, err := store.CreateOrg(t.Context(), state.Org{Slug: "irreversible-co", Name: "Irreversible", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(t.Context(), org.ID, acct.ID, state.OrgRoleOwner, nil); err != nil {
		t.Fatal(err)
	}
	apidserver.AgeSignInsForTest(t, api.RecentSignInWindow+time.Minute)
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"delete account": flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodDelete, "/v1/account", "", nil),
		"transfer ownership": flowSessionJSON(t, h, mgr, cookie, acct.ID, http.MethodPost, "/v1/orgs/"+org.Slug+"/transfer_ownership", "",
			map[string]any{"new_owner_account_id": "00000000-0000-0000-0000-000000000001"}),
	} {
		if rec.Code != http.StatusForbidden || problemCode(t, rec.Body.Bytes()) != api.CodeRecentSignInRequired {
			t.Errorf("%s from a stale session = %d %s, want 403 %s", name, rec.Code, rec.Body.String(), api.CodeRecentSignInRequired)
		}
	}
	if fresh, err := store.AccountByID(t.Context(), acct.ID); err != nil || fresh.Status != state.AccountActive {
		t.Fatalf("a refused deletion changed the account: %+v %v", fresh.Status, err)
	}
}
