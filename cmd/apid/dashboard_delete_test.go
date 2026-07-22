// Tests for the G6 dashboard delete/restore forms (cmd/apid/dashboard_delete.go).
//
// The handlers are session-authed (faas_sid cookie) and live behind the
// dashboard middleware. The harness from handlers_dashboard_test.go (newAuthedDashboardServer)
// already wires a real session.Manager, mints a cookie for a fresh test account,
// and returns the full http.Handler — so these tests just need to attach the
// cookie and POST with the right form body.
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

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardPOST posts a form to the given path with the supplied fields.
// The harness's session cookie is automatically attached.
func dashboardPOST(t *testing.T, h http.Handler, cookie *http.Cookie, path string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	for k, v := range fields {
		form.Set(k, v)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestDashboardDelete_HappyPath posts the confirmation token and expects a
// 302 redirect to /dashboard/account?deleted=1.
func TestDashboardDelete_HappyPath(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := dashboardPOST(t, srv, cookie, "/dashboard/account/delete",
		map[string]string{"confirm_token": "delete:yes"})
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302\nbody = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "deleted=1") {
		t.Errorf("Location = %q, want deleted=1", loc)
	}
}

// TestDashboardDelete_BadToken rejects a mismatched confirmation token with
// 400. The dashboard template renders the matching "<action>:yes" string;
// any other value (or empty form field) must be rejected.
func TestDashboardDelete_BadToken(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := dashboardPOST(t, srv, cookie, "/dashboard/account/delete",
		map[string]string{"confirm_token": "delete:no"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardDelete_RestoreRejectsDeleteToken confirms the token is bound
// to the action: a "delete:yes" token replayed against /restore must fail.
// This is the core CSRF defence from the confirmTokenMatches docstring.
func TestDashboardDelete_RestoreRejectsDeleteToken(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := dashboardPOST(t, srv, cookie, "/dashboard/account/restore",
		map[string]string{"confirm_token": "delete:yes"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (cross-action token rejected)\nbody = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboardRestore_BadToken confirms the token-validation path on the
// restore endpoint. The happy path is structurally unreachable from the
// browser because sessionAuth redirects deletion-pending accounts to
// /login (handlers_auth.go:230); the cancel flow is exercised via the
// REST endpoint POST /v1/account/restore in handlers_account_test.go.
func TestDashboardRestore_BadToken(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)
	rec := dashboardPOST(t, srv, cookie, "/dashboard/account/restore",
		map[string]string{"confirm_token": "wrong"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400\nbody = %s", rec.Code, rec.Body.String())
	}
}

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

// TestConfirmTokenMatches_PureUnit exercises the helper directly without
// the full server — useful for the parse-error and empty-token branches.
func TestConfirmTokenMatches_PureUnit(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		action  string
		want    bool
	}{
		{"happy", "confirm_token=delete%3Ayes", "delete", true},
		{"empty token", "", "delete", false},
		{"wrong action", "confirm_token=delete%3Ayes", "restore", false},
		{"truncated", "confirm_token=delete", "delete", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if got := confirmTokenMatches(r, tc.action); got != tc.want {
				t.Errorf("confirmTokenMatches = %v, want %v", got, tc.want)
			}
		})
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