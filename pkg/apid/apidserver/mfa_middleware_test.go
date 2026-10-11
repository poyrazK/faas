// requireMFA middleware matrix (IAM-2, issue #186).
//
// Pins the gate's truth table: the cookie branch stamps
// mfa_pending on the context; the bearer branch doesn't; the
// MFA allowlist must let the enrollment / step-up / recovery /
// disable routes through; everything else 403s with
// CodeMFARequired while pending.

package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"

	"github.com/google/uuid"
)

// reissueWithMFAFlag issues a fresh cookie with mfa_pending set
// to the desired value. The cookie must carry a sid backed by a
// live sessions row, otherwise requireSessionCookie rejects it
// with CodeSessionExpired (IAM-3 / ADR-039). The login handlers
// use the same IssueWithSession path after the account opts into
// MFA or an explicit policy is enabled.
func reissueWithMFAFlag(t *testing.T, mgr *session.Manager, sid, accountID string, pending bool) *http.Cookie {
	t.Helper()
	tok, err := mgr.IssueWithSession(sid, accountID, pending)
	if err != nil {
		t.Fatalf("IssueWithSession: %v", err)
	}
	return &http.Cookie{Name: sessionCookie, Value: tok}
}

// cookieDo builds a request with the given cookie + body and
// runs it through the handler chain. body=nil means no body;
// the Content-Type header is set only when a body is present.
// When csrfMgr is non-nil and the route gates on CSRF (per
// issue #186 Finding #7), it injects the matching token so the
// inner handler reaches its body-decoding branch instead of
// short-circuiting with 403 "Invalid CSRF token".
func cookieDo(t *testing.T, h http.Handler, cookie *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	return cookieDoCSRF(t, h, cookie, nil, "", method, path, body)
}

// cookieDoCSRF is the explicit-shape variant used by tests that
// have the session.Manager in scope (the middleware tests pass
// it through here so the CSRF token's subject matches the
// cookie's account_id).
func cookieDoCSRF(t *testing.T, h http.Handler, cookie *http.Cookie, csrfMgr *session.Manager, csrfAcctID, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	rawBody := []byte(nil)
	if body != nil {
		var err error
		rawBody, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(rawBody)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
		// CSRF: only when a manager is supplied AND the route
		// gates on CSRF. The middleware tests that don't care
		// about inner handler bodies (e.g. testing the 403
		// surface) leave csrfMgr nil and skip this branch.
		if csrfMgr != nil {
			if action := csrfAction(path); action != "" {
				tok, err := middleware.IssueForAuthenticated(csrfMgr, action, csrfAcctID)
				if err != nil {
					t.Fatalf("issue CSRF for %s: %v", action, err)
				}
				req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
				if len(rawBody) > 0 && rawBody[0] == '{' {
					wrapped := injectCSRFToken(rawBody, tok)
					req.Body = io.NopCloser(bytes.NewReader(wrapped))
					req.ContentLength = int64(len(wrapped))
				}
			}
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// setupMW spins up a server with an ephemeral session manager
// (no in-process age key — the middleware tests don't exercise
// the seal path) and a no-op ops registry. Returns the server
// handler plus the account + manager + sid so the test can
// mint cookies with custom mfa_pending values.
//
// IAM-3 (ADR-039): the sid is stamped into a fresh sessions
// row so requireSessionCookie accepts the resulting cookie. The
// login helpers do the same — there is no path where the cookie
// branch accepts a sid-less envelope.
func setupMW(t *testing.T, plan api.Plan, mfaRequired bool) (http.Handler, state.Account, *session.Manager, string) {
	t.Helper()
	// Recovery-code HMAC upgrade: the production loader wires the
	// per-process secret at apid boot (cmd/apid/main.go::loadOrGenerateRecoveryHMACKey);
	// the test fixture has no boot path, so the shared sync.Once
	// helper installs a deterministic test secret once across the
	// suite. setupMW doesn't itself call authcode, but tests that
	// compose around it might — installing the secret here means
	// the suite-level invariant holds regardless of which setup
	// the caller used.
	ensureRecoveryTestSecret(t)
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(),
		"mw@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	if mfaRequired {
		if _, err := store.SetMFARequired(context.Background(), acct.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID,
		"192.0.2.30", "mfa-mw-test-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	ops := wire.NewOpsMetrics("apid_mfa_middleware_test")
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), ops)
	return srv.handler(), acct, mgr, sid
}

// --- the matrix ------------------------------------------------------------

// TestRequireMFA_BearerBypasses pins decision 3: API keys
// (bearer) are never MFA-gated. Mint a Pro account, fire a
// bearer-key GET on a non-allowlisted route, and confirm the
// response is NOT 403.
//
// This locks the bug class where a future edit could move
// requireMFA inside the auth middleware so it stamps the flag
// for both branches — the test would start to fail with 403.
func TestRequireMFA_BearerBypasses(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "bearer@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetMFARequired(context.Background(), acct.ID, true); err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "bypass-test", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_mfa_bypass_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)

	// Bearer key + non-allowlisted route. The requireMFA
	// middleware must NOT 403.
	req := httptest.NewRequest(http.MethodGet, "/v1/apps", nil)
	req.Header.Set("Authorization", "Bearer "+pt)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Errorf("bearer was 403'd on /v1/apps: %s", rec.Body)
	}
}

// TestRequireMFA_PendingSessionBlocksNonAllowlist pins the core
// 403 path: a session cookie stamped mfa_pending=true hitting a
// non-allowlisted route returns 403 CodeMFARequired.
func TestRequireMFA_PendingSessionBlocksNonAllowlist(t *testing.T) {
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	pendingCookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)

	for _, path := range []string{"/v1/apps", "/v1/usage", "/v1/keys"} {
		rec := cookieDo(t, h, pendingCookie, http.MethodGet, path, nil)
		if rec.Code != http.StatusForbidden {
			t.Errorf("path %s: status = %d, want 403", path, rec.Code)
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err == nil {
			if prob.Code != api.CodeMFARequired {
				t.Errorf("path %s: code = %q, want %q", path, prob.Code, api.CodeMFARequired)
			}
		}
	}
}

// TestRequireMFA_PendingSessionAllowedOnAllowlist pins the
// allowlist: mfa_pending=true can still reach the dashboard
// "you need MFA" prompt + the step-up / recovery / disable
// routes. /enroll is covered by handlers_mfa_test.go (the
// middleware tests don't wire an age recipient, which /enroll
// requires to seal the secret).
func TestRequireMFA_PendingSessionAllowedOnAllowlist(t *testing.T) {
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	pendingCookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)

	// Each row is a (/method, /path, body) check. None should be
	// 403. The exact status depends on the body — the loader
	// routes return 200 (account), 401 (verify/recover, no
	// secret), 400 (disable, no body).
	allowlist := []struct {
		method, path string
		body         any
	}{
		{http.MethodGet, "/v1/account", nil},
		{http.MethodPost, "/v1/account/mfa/verify", api.MFAVerifyRequest{}},
		{http.MethodPost, "/v1/account/mfa/recover", api.MFARecoverRequest{}},
		{http.MethodPost, "/v1/account/mfa/disable", api.MFADisableRequest{}},
	}
	for _, c := range allowlist {
		rec := cookieDoCSRF(t, h, pendingCookie, mgr, acct.ID, c.method, c.path, c.body)
		if rec.Code == http.StatusForbidden {
			t.Errorf("allowlist %s %s: 403 (CodeMFARequired leaked); body=%s",
				c.method, c.path, rec.Body)
		}
	}
}

// TestRequireMFA_ClearedSessionPassesThrough confirms the
// post-enrollment state: a cookie with mfa_pending=false
// passes through requireMFA on a non-allowlisted route. Pin
// the bug class where a future edit drops the `if !pending`
// short-circuit.
func TestRequireMFA_ClearedSessionPassesThrough(t *testing.T) {
	h, acct, mgr, sid := setupMW(t, api.PlanPro, false)
	clearedCookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, false)

	rec := cookieDo(t, h, clearedCookie, http.MethodGet, "/v1/apps", nil)
	if rec.Code == http.StatusForbidden {
		t.Errorf("mfa_pending=false was 403'd on /v1/apps: %s", rec.Body)
	}
}

// TestRequireMFA_403BodyShape pins the RFC 7807 contract: the
// 403 body is a problem with code=mfa_required. The dashboard
// branches on this exact string to render the "complete MFA"
// prompt.
func TestRequireMFA_403BodyShape(t *testing.T) {
	h, acct, mgr, sid := setupMW(t, api.PlanPro, true)
	pendingCookie := reissueWithMFAFlag(t, mgr, sid, acct.ID, true)

	rec := cookieDo(t, h, pendingCookie, http.MethodGet, "/v1/apps", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), api.CodeMFARequired) {
		t.Errorf("body missing %q: %s", api.CodeMFARequired, rec.Body)
	}
	var prob api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prob.Code != api.CodeMFARequired {
		t.Errorf("code = %q, want %q", prob.Code, api.CodeMFARequired)
	}
	if prob.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", prob.Status)
	}
}
