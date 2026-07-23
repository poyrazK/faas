// Tests for the G6 dashboard delete/restore forms (cmd/apid/dashboard_delete.go,
// security review A3).
//
// The handlers are session-authed (faas_sid cookie) and live behind the
// dashboard middleware. The harness from handlers_dashboard_test.go
// (newAuthedDashboardServer) wires a real session.Manager, mints a cookie
// for a fresh test account, and returns the full http.Handler. These
// tests additionally need the faas_csrf cookie set on a prior
// /dashboard/account render, so we drive GET → POST when the test needs
// a fresh sealed pair.
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardPOST posts a form to the given path with the supplied fields.
// Extra cookies can be attached via extraCookies (e.g. the faas_csrf
// sidecar). The harness's faas_sid cookie is always attached.
func dashboardPOST(t *testing.T, h http.Handler, sid *http.Cookie, path string, fields map[string]string, extraCookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sid != nil {
		req.AddCookie(sid)
	}
	for _, c := range extraCookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// renderDashboardAccount GETs /dashboard/account with the session cookie
// and returns the rendered form's csrf_token value + the matching faas_csrf
// sidecar cookie. Used by tests that need a fresh sealed envelope
// (review finding A3: the envelope is bound to account_id, so the test
// must drive a real render to get a valid pair).
func renderDashboardAccount(t *testing.T, h http.Handler, sid *http.Cookie) (csrfCookie, deleteToken, restoreToken string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account", nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/account: status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == middleware.CookieNameAuthenticated {
			csrfCookie = c.Value
		}
	}
	if csrfCookie == "" {
		t.Fatalf("GET /dashboard/account: missing %s cookie in Set-Cookie: %v",
			middleware.CookieNameAuthenticated, rec.Header().Get("Set-Cookie"))
	}
	// Both forms share the same csrf_token field name; in production
	// only one form is rendered per page (showDelete XOR showRestore)
	// — but renderAccount mints BOTH tokens and binds both into the
	// same cookie. We grab whichever input shows up. The dashboard
	// template uses `csrf_token` for both forms (A3 renames the field
	// from the old `confirm_token`).
	body := rec.Body.String()
	deleteToken = extractInputValue(body, "csrf_token", "/dashboard/account/delete")
	restoreToken = extractInputValue(body, "csrf_token", "/dashboard/account/restore")
	return csrfCookie, deleteToken, restoreToken
}

// extractInputValue finds `name="csrf_token" value="…"` immediately
// after the form's action attribute. The template emits the
// csrf_token hidden field AFTER the <form action=…> opener, so a
// forward scan starting at the action marker disambiguates the two
// values (delete form vs restore form).
func extractInputValue(body, field, action string) string {
	idx := strings.Index(body, `action="`+action+`"`)
	if idx < 0 {
		return ""
	}
	// Find the csrf_token input AFTER the action attribute. This is
	// robust whether the template emits the input before or after
	// the action marker, because the field lives inside the same
	// <form> element either way.
	rest := body[idx:]
	rel := strings.Index(rest, `name="`+field+`" value="`)
	if rel < 0 {
		return ""
	}
	start := idx + rel + len(`name="`+field+`" value="`)
	// Limit the end search to the same form element (until </form>).
	formEnd := strings.Index(body[start:], `</form>`)
	var end int
	if formEnd < 0 {
		end = strings.Index(body[start:], `"`)
	} else {
		end = strings.Index(body[start:start+formEnd], `"`)
	}
	if end < 0 {
		return ""
	}
	return body[start : start+end]
}

// TestDashboardDelete_HappyPath drives a real GET → POST pair. The
// renderer mints the faas_csrf cookie + delete csrf_token field; the
// handler verifies and 302s to ?deleted=1.
func TestDashboardDelete_HappyPath(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	csrfCookie, deleteToken, _ := renderDashboardAccount(t, srv, sid)
	if deleteToken == "" {
		t.Fatal("rendered account page is missing the delete csrf_token")
	}
	rec := dashboardPOST(t, srv, sid, "/dashboard/account/delete",
		map[string]string{middleware.FormFieldName: deleteToken},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "deleted=1") {
		t.Errorf("Location = %q, want deleted=1", loc)
	}
}

// TestDashboardDelete_BadToken rejects a forged token with 400. A
// cross-site attacker who manages to send a SameSite=Lax cookie
// (e.g. via a top-level navigation) still cannot forge a sealed
// envelope without the 32-byte session key.
func TestDashboardDelete_BadToken(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	csrfCookie, _, _ := renderDashboardAccount(t, srv, sid)
	rec := dashboardPOST(t, srv, sid, "/dashboard/account/delete",
		map[string]string{middleware.FormFieldName: "forged-value"},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardDelete_RequiresCSRFFromSession asserts the cross-site
// attack path: the attacker does NOT have the faas_csrf cookie
// (their own browser session doesn't carry it). The handler rejects
// with 400 because the helper requires both the cookie and the form
// field, with byte-equal values.
func TestDashboardDelete_RequiresCSRFFromSession(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	_, deleteToken, _ := renderDashboardAccount(t, srv, sid)
	// POST without the faas_csrf cookie. This simulates a cross-site
	// attacker who tricks the victim's browser into POSTing the form
	// without the sidecar cookie.
	rec := dashboardPOST(t, srv, sid, "/dashboard/account/delete",
		map[string]string{middleware.FormFieldName: deleteToken})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no faas_csrf cookie)\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardRestore_RejectsForeignCSRF confirms the token is bound
// to the action: a delete-bound token replayed against /restore must
// fail. Even with the right cookie, the action mismatch causes the
// helper to reject.
func TestDashboardRestore_RejectsForeignCSRF(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	csrfCookie, deleteToken, _ := renderDashboardAccount(t, srv, sid)
	rec := dashboardPOST(t, srv, sid, "/dashboard/account/restore",
		map[string]string{middleware.FormFieldName: deleteToken},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (cross-action token rejected)\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardRestore_BadToken confirms the helper rejects a forged
// form value on the restore endpoint with 400.
func TestDashboardRestore_BadToken(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	csrfCookie, _, _ := renderDashboardAccount(t, srv, sid)
	rec := dashboardPOST(t, srv, sid, "/dashboard/account/restore",
		map[string]string{middleware.FormFieldName: "forged-value"},
		&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: csrfCookie})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardRestore_HappyPath is intentionally not exercised
// here. sessionAuth (server.go:269) redirects deletion-pending
// accounts to /login before the dashboard renders (see
// handlers_auth.go:230), so a GET /dashboard/account after scheduling
// deletion cannot reach renderAccount. The cancel flow is exercised
// via the REST endpoint POST /v1/account/restore in
// handlers_account_test.go. The CSRF defense on /dashboard/account/restore
// is covered by TestDashboardRestore_BadToken and
// TestDashboardRestore_RejectsForeignCSRF above.

// TestDashboardExport_HappyPath confirms the session-authed export returns
// the JSON bundle (same shape as the REST /v1/account/export).
func TestDashboardExport_HappyPath(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account/export", nil)
	req.AddCookie(cookie)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	if disp := rec.Header().Get("Content-Disposition"); !strings.Contains(disp, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", disp)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "alice@example.com") {
		t.Errorf("export body missing the seeded account email:\n%s", body)
	}
}

// TestDashboardExport_IncludeSecretsFalse exercises the ?include_secrets=false
// branch that mirrors the REST endpoint.
func TestDashboardExport_IncludeSecretsFalse(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account/export?include_secrets=false", nil)
	req.AddCookie(cookie)
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardDelete_CSRFHelperBranch exercises the helper boundary
// directly through a synthetic request so the parse-error and
// empty-token branches stay covered without the full server. The
// helper itself (IssueForAuthenticated/VerifyAuthenticated) is
// exhaustively tested in pkg/middleware/csrf_test.go; here we just
// assert that the dashboardDelete handler surfaces the helper's
// 400 response when called with no body.
func TestDashboardDelete_CSRFHelperBranch(t *testing.T) {
	srv, sid := newAuthedDashboardServer(t)
	// POST with neither faas_csrf cookie nor csrf_token form field —
	// the helper returns ErrCSRFInvalid; the handler maps that to 400.
	rec := dashboardPOST(t, srv, sid, "/dashboard/account/delete", map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("empty body: status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardDPA_HappyPath writes a fake DPA template to a temp dir,
// wires it into the server via newServerWithDeps, and confirms the
// dashboard wrapper renders 200 with the markdown content in the body.
func TestDashboardDPA_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	dpa := filepath.Join(tmp, "dpa.md")
	if err := os.WriteFile(dpa, []byte("# DPA\nHello customer."), 0o600); err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(context.Background(), "dpa@example.com", "free")
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	token, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	cookie := &http.Cookie{Name: sessionCookie, Value: token}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil,
		15*60_000_000_000, dpa)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account/dpa", nil)
	req.AddCookie(cookie)
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Hello customer.") {
		t.Errorf("DPA body missing content\n%s", rec.Body.String())
	}
}

// TestDashboardDPA_MissingTemplate confirms the 503 when the DPA file is
// not installed (s.dpaPath == "").
func TestDashboardDPA_MissingTemplate(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/account/dpa", nil)
	req.AddCookie(cookie)
	srv.ServeHTTP(rec, req)
	// newAuthedDashboardServer passes "" for dpaPath → 503.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestAcctViewFrom_PureUnit exercises the trivial converter.
func TestAcctViewFrom_PureUnit(t *testing.T) {
	acct := state.Account{ID: "a1", Email: "x@example.com", Plan: "pro"}
	v := acctViewFrom(acct)
	if v.ID != "a1" || v.Email != "x@example.com" || v.Plan != "pro" {
		t.Errorf("got %+v", v)
	}
}
