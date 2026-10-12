// IAM-3 (ADR-039, issue #187 + #244 merged) handler tests.
// Table-driven over the four routes + the cookie-branch failure
// modes. Mirrors the handlers_mfa_test.go harness; uses MemStore
// everywhere (the in-memory backend still has to comply with the
// production pgstore contract).
package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sessionTestEnv wires an account + a MemStore + a session.Manager
// + a server, and lets the test mint additional sessions (sibling
// logins). The cookie returned is the AEAD-bound envelope whose
// sid stamps a sessions row in the store. The audit handle is
// wired by newServer internally; tests can read event rows via
// store.ListEventsForAccount.
type sessionTestEnv struct {
	h         http.Handler
	srv       *server
	store     *state.MemStore
	acct      state.Account
	mgr       *session.Manager
	cookie    *http.Cookie
	currentID string // sid stamped into the cookie envelope
}

// newSessionEnv is the test-side analog of what
// issueDashboardSession does: mint sid, persist row, seal
// envelope. We avoid calling the helper directly so the audit
// emitter stays out of the test's scope (each test asserts its
// own audit behaviour).
func newSessionEnv(t *testing.T) sessionTestEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "iam3@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID,
		"192.0.2.10", "iam3-test-ua"); err != nil {
		t.Fatal(err)
	}
	cookieVal, err := mgr.IssueWithSession(sid, acct.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), wire.NewOpsMetrics("apid_iam3_test"))
	return sessionTestEnv{
		h:         srv.handler(),
		srv:       srv,
		store:     store,
		acct:      acct,
		mgr:       mgr,
		cookie:    &http.Cookie{Name: sessionCookie, Value: cookieVal},
		currentID: sid,
	}
}

// mintSiblingSeal mints a NEW session for the same account and
// returns the cookie + sid. Used by sibling-independence tests.
func mintSiblingSeal(t *testing.T, env sessionTestEnv) (*http.Cookie, string) {
	t.Helper()
	sid := uuid.NewString()
	if _, err := env.store.CreateSession(context.Background(), sid, env.acct.ID,
		"203.0.113.5", "iam3-sibling-ua"); err != nil {
		t.Fatal(err)
	}
	v, err := env.mgr.IssueWithSession(sid, env.acct.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: v}, sid
}

// doSession is the request dispatch helper. The body's env stays
// in scope (a subtest-local closure) so the dispatch reads the
// right `h` per call. Body is optional (nil = no body).
//
// CSRF is auto-injected for the three gated routes:
//   - POST   /v1/auth/logout
//   - DELETE /v1/auth/sessions/{id}
//   - POST   /v1/auth/sessions/revoke_all
//
// The csrfActionName helper below maps the path to the action
// the handler verifies against; the cookie + body
// `csrf_token` field are populated using IssueForAuthenticated
// + injectCSRFToken so the wire shape matches what the dashboard
// sends.
func doSession(t *testing.T, env sessionTestEnv, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var rawBody []byte
	var r io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rawBody = buf
		r = bytes.NewReader(buf)
	}
	req := httptest.NewRequest(method, path, r)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if action := csrfActionName(path); action != "" {
		tok, err := middleware.IssueForAuthenticated(env.mgr, action, env.acct.ID)
		if err != nil {
			t.Fatalf("issue CSRF token for %s: %v", action, err)
		}
		req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
		// DELETE /v1/auth/sessions/{id} carries no body — but
		// verifyAgainstRequest reads csrf_token via either form
		// or JSON. We pass it as a URL query parameter for
		// DELETE; for POST we wrap the JSON body.
		switch method {
		case http.MethodDelete:
			// CSRF extract reads either form (with form
			// Content-Type) or JSON body. The form path
			// requires the request body to be
			// io.NopCloser-wrapped at the time
			// r.ParseForm is called inside the
			// middleware; httptest's request setup
			// doesn't always reproduce that shape, so we
			// use the JSON-body path with
			// {csrf_token: <tok>} as the body. The DELETE
			// handler ignores body so this is wire-safe.
			body := []byte(`{"csrf_token":"` + tok + `"}`)
			req.Header.Set("Content-Type", "application/json")
			req.Body = io.NopCloser(bytes.NewReader(body))
			req.ContentLength = int64(len(body))
		default:
			if len(rawBody) > 0 && rawBody[0] == '{' {
				wrapped := injectCSRFToken(rawBody, tok)
				req.Body = io.NopCloser(bytes.NewReader(wrapped))
				req.ContentLength = int64(len(wrapped))
			} else {
				// Empty body POST — wrap {csrf_token: tok}
				req.Body = io.NopCloser(bytes.NewReader([]byte(
					`{"csrf_token":"` + tok + `"}`,
				)))
				req.ContentLength = int64(len(`{"csrf_token":"` + tok + `"}`))
				req.Header.Set("Content-Type", "application/json")
			}
		}
	}
	rec := httptest.NewRecorder()
	env.h.ServeHTTP(rec, req)
	return rec
}

// csrfActionName maps the IAM-3 routes to their CSRF action
// strings. Mirrors the constants in handlers_sessions.go.
func csrfActionName(path string) string {
	if strings.HasPrefix(path, "/v1/auth/sessions/") &&
		!strings.HasSuffix(path, "/sessions") &&
		!strings.HasSuffix(path, "/revoke_all") {
		return csrfActionSessionRevoke
	}
	switch {
	case strings.HasSuffix(path, "/v1/auth/logout"):
		return csrfActionLogout
	case strings.HasSuffix(path, "/v1/auth/sessions/revoke_all"):
		return csrfActionSessionsRevokeAll
	}
	return ""
}

// problemHasCode decodes the RFC 7807 problem from body and
// returns true when the `code` field equals want.
func problemHasCode(t *testing.T, body []byte, want string) bool {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		return false
	}
	got, _ := p["code"].(string)
	return got == want
}

// cookieCleared returns true when the response's cookies include a
// MaxAge<0 or empty-value entry for sessionCookie (clearSessionCookie
// sets MaxAge=-1 + Value="").
func cookieCleared(t *testing.T, cookies []*http.Cookie) bool {
	t.Helper()
	for _, c := range cookies {
		if c.Name == sessionCookie {
			if c.MaxAge < 0 || c.Value == "" {
				return true
			}
		}
	}
	return false
}

// TestHandlers_Sessions_ListReturnsCurrentOnlyOnFirstLogin is the
// simplest IAM-3 happy path: one login → one session row, flagged
// current.
func TestHandlers_Sessions_ListReturnsCurrentOnlyOnFirstLogin(t *testing.T) {
	env := newSessionEnv(t)
	rec := doSession(t, env, "GET", "/v1/auth/sessions", nil, env.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out api.SessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Sessions) != 1 {
		t.Fatalf("sessions count = %d, want 1", len(out.Sessions))
	}
	if !out.Sessions[0].CurrentSession {
		t.Errorf("CurrentSession = false on the only login; want true")
	}
	if out.Sessions[0].ID != env.currentID {
		t.Errorf("ID = %q, want %q", out.Sessions[0].ID, env.currentID)
	}
}

// TestHandlers_Sessions_ListFlagsOnlyCallingSidCurrent confirms the
// newest-first ordering + the current_session flag is set only on
// the calling sid.
func TestHandlers_Sessions_ListFlagsOnlyCallingSidCurrent(t *testing.T) {
	env := newSessionEnv(t)
	_, sibling := mintSiblingSeal(t, env)
	rec := doSession(t, env, "GET", "/v1/auth/sessions", nil, env.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var out api.SessionListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Sessions) != 2 {
		t.Fatalf("count = %d, want 2", len(out.Sessions))
	}
	if out.Sessions[0].ID != sibling {
		t.Errorf("list[0].ID = %q, want %q (newest first)", out.Sessions[0].ID, sibling)
	}
	if out.Sessions[0].CurrentSession {
		t.Errorf("list[0] (sibling) flagged current; want false")
	}
	if out.Sessions[1].ID != env.currentID {
		t.Errorf("list[1].ID = %q, want %q", out.Sessions[1].ID, env.currentID)
	}
	if !out.Sessions[1].CurrentSession {
		t.Errorf("list[1] (calling) not current; want true")
	}
}

// TestHandlers_Sessions_RevokeSibling_Returns204_SiblingExpired
// pins the sibling-independence invariant (the load-bearing reason
// #244 was chosen over #187). Revoking sibling:
//   - returns 204 to the caller
//   - the caller's session stays alive
//   - the sibling's next request 401s with CodeSessionExpired and
//     the cookie is cleared
func TestHandlers_Sessions_RevokeSibling_Returns204_SiblingExpired(t *testing.T) {
	env := newSessionEnv(t)
	siblingCookie, sibling := mintSiblingSeal(t, env)

	rec := doSession(t, env, "DELETE", "/v1/auth/sessions/"+sibling, nil, env.cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}

	listRec := doSession(t, env, "GET", "/v1/auth/sessions", nil, env.cookie)
	if listRec.Code != http.StatusOK {
		t.Fatalf("calling session status = %d, want 200", listRec.Code)
	}

	sibRec := doSession(t, env, "GET", "/v1/auth/sessions", nil, siblingCookie)
	if sibRec.Code != http.StatusUnauthorized {
		t.Fatalf("sibling status = %d, want 401", sibRec.Code)
	}
	if !problemHasCode(t, sibRec.Body.Bytes(), api.CodeSessionExpired) {
		t.Errorf("sibling body code != session_expired (body=%s)", sibRec.Body.String())
	}
	if !cookieCleared(t, sibRec.Result().Cookies()) {
		t.Errorf("faas_sid cookie not cleared on 401")
	}
}

// TestHandlers_Sessions_RevokeAll_ExceptCurrent_CallingUntouched.
func TestHandlers_Sessions_RevokeAll_ExceptCurrent_CallingUntouched(t *testing.T) {
	env := newSessionEnv(t)
	_, sibling1 := mintSiblingSeal(t, env)
	_, sibling2 := mintSiblingSeal(t, env)

	rec := doSession(t, env, "POST", "/v1/auth/sessions/revoke_all", nil, env.cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var out api.SessionsRevokeAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Revoked != 2 {
		t.Errorf("Revoked = %d, want 2 (two siblings)", out.Revoked)
	}

	if listRec := doSession(t, env, "GET", "/v1/auth/sessions", nil, env.cookie); listRec.Code != http.StatusOK {
		t.Errorf("calling session after revoke_all = %d, want 200", listRec.Code)
	}
	for _, sid := range []string{sibling1, sibling2} {
		row, err := env.store.GetSession(context.Background(), sid)
		if err != nil {
			t.Fatalf("GetSession %s: %v", sid, err)
		}
		if row.RevokedAt == nil {
			t.Errorf("sibling %s not revoked", sid)
		}
	}
}

// TestHandlers_Sessions_Logout_ClearsCookie_RowRevoked.
func TestHandlers_Sessions_Logout_ClearsCookie_RowRevoked(t *testing.T) {
	env := newSessionEnv(t)
	rec := doSession(t, env, "POST", "/v1/auth/logout", nil, env.cookie)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
	}
	if !cookieCleared(t, rec.Result().Cookies()) {
		t.Errorf("faas_sid cookie not cleared on logout")
	}
	row, err := env.store.GetSession(context.Background(), env.currentID)
	if err != nil {
		t.Fatal(err)
	}
	if row.RevokedAt == nil {
		t.Errorf("current session not revoked by logout")
	}
	next := doSession(t, env, "GET", "/v1/auth/sessions", nil, env.cookie)
	if next.Code != http.StatusUnauthorized {
		t.Errorf("post-logout status = %d, want 401", next.Code)
	}
}

// TestHandlers_Sessions_RevokeCrossAccount_404_NoLeak is the IDOR
// guard proof: revoking another account's sid returns 404, NOT
// 403 (we never confirm a row exists in another account) and
// leaves the other row untouched.
func TestHandlers_Sessions_RevokeCrossAccount_404_NoLeak(t *testing.T) {
	env := newSessionEnv(t)
	other, err := env.store.CreateAccount(context.Background(),
		fmt.Sprintf("cross-%s@example.com", uuid.NewString()), "free")
	if err != nil {
		t.Fatal(err)
	}
	otherSid := uuid.NewString()
	if _, err := env.store.CreateSession(context.Background(),
		otherSid, other.ID, "198.51.100.1", "iam3-other-ua"); err != nil {
		t.Fatal(err)
	}
	rec := doSession(t, env, "DELETE", "/v1/auth/sessions/"+otherSid, nil, env.cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (IDOR guard; body=%s)", rec.Code, rec.Body.String())
	}
	if !problemHasCode(t, rec.Body.Bytes(), api.CodeNotFound) {
		t.Errorf("expected not_found; got: %s", rec.Body.String())
	}
	row, _ := env.store.GetSession(context.Background(), otherSid)
	if row.RevokedAt != nil {
		t.Errorf("cross-account row was revoked; IDOR guard breached")
	}
}

// TestHandlers_Sessions_RevokeMissingId_404_NoLeak ensures we
// never differentiate "not found" from "never existed".
func TestHandlers_Sessions_RevokeMissingId_404_NoLeak(t *testing.T) {
	env := newSessionEnv(t)
	missing := uuid.NewString()
	rec := doSession(t, env, "DELETE", "/v1/auth/sessions/"+missing, nil, env.cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestHandlers_Sessions_RevokeRejectsNonUUID — defence in depth:
// even though the SQL parameter is bounded, the handler validates
// the path id.
func TestHandlers_Sessions_RevokeRejectsNonUUID(t *testing.T) {
	env := newSessionEnv(t)
	rec := doSession(t, env, "DELETE", "/v1/auth/sessions/not-a-uuid", nil, env.cookie)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !problemHasCode(t, rec.Body.Bytes(), api.CodeValidation) {
		t.Errorf("want validation_failed; got: %s", rec.Body.String())
	}
}

// TestHandlers_Sessions_PreIAM3Cookie_401_CookieCleared is the
// rollout invalidation proof: an old cookie (no sid) returns 401
// with CodeSessionExpired and evicts the cookie on the wire. This
// is the load-bearing behaviour for the rollout.
func TestHandlers_Sessions_PreIAM3Cookie_401_CookieCleared(t *testing.T) {
	env := newSessionEnv(t)
	legacyCookieVal, err := env.mgr.IssueWithMFAFlag(env.acct.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	legacy := &http.Cookie{Name: sessionCookie, Value: legacyCookieVal}
	rec := doSession(t, env, "GET", "/v1/auth/sessions", nil, legacy)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !problemHasCode(t, rec.Body.Bytes(), api.CodeSessionExpired) {
		t.Errorf("want session_expired; got: %s", rec.Body.String())
	}
	if !cookieCleared(t, rec.Result().Cookies()) {
		t.Errorf("faas_sid cookie not cleared on pre-IAM-3 cookie rejection")
	}
}

// TestHandlers_Sessions_RevokedSidCookie_401 confirms a previously-
// valid sid that got revoked mid-flight returns CodeSessionExpired.
func TestHandlers_Sessions_RevokedSidCookie_401(t *testing.T) {
	env := newSessionEnv(t)
	if _, err := env.store.RevokeSession(context.Background(),
		env.currentID, env.acct.ID); err != nil {
		t.Fatal(err)
	}
	rec := doSession(t, env, "GET", "/v1/auth/sessions", nil, env.cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if !problemHasCode(t, rec.Body.Bytes(), api.CodeSessionExpired) {
		t.Errorf("want session_expired; got: %s", rec.Body.String())
	}
}

// TestRequireSessionCookie_DefendsAccountMismatch covers the
// defensive branch at session_middleware.go (sess.AccountID !=
// env.AccountID → CodeSessionInvalid + cookie cleared). The
// branch is unreachable on honest mints because the AEAD
// envelope binds AccountID + Sid in the same ciphertext, but if
// a future key rotation or impl bug ever decoupled them we'd
// want the cross-check. Without this test a regression would
// silently clear the cookie and 401 with CodeSessionExpired
// instead.
//
// The forge: two accounts in a MemStore, a real sessions row
// stamped with sid → accountA, then an envelope sealed with
// the same sid BUT accountID=accountB. We use mgr.Seal to
// construct the forged envelope — Manager.Issue/IssueWithSession
// take a single accountID parameter and can't produce a
// cross-account envelope, but Manager.Seal accepts a hand-built
// Envelope. (Manager.Seal is the public primitive that
// pkg/session exposes precisely for this kind of test seam.)
func TestRequireSessionCookie_DefendsAccountMismatch(t *testing.T) {
	store := state.NewMemStore()
	acctA, err := store.CreateAccount(context.Background(), "a-forge@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount A: %v", err)
	}
	acctB, err := store.CreateAccount(context.Background(), "b-forge@example.com", "free")
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("NewEphemeralManager: %v", err)
	}
	// Real sessions row stamped sid → acctA.
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acctA.ID,
		"192.0.2.30", "forge-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Forged envelope: same sid, but AEAD-bound AccountID = acctB.
	// This is the precise shape that would arise from a key-rotation
	// or impl bug: cookie says "I belong to B", row says "row belongs
	// to A". requireSessionCookie MUST reject.
	forged, err := mgr.Seal(session.Envelope{
		AccountID:  acctB.ID,
		IssuedAt:   time.Now(),
		ExpiresAt:  time.Now().Add(sessionCookieLifetime),
		MfaPending: false,
		Sid:        sid,
	})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// Now drive the middleware directly. We can't go through
	// doSession because the cookie needs to *fail* in
	// requireSessionCookie — doSession only knows how to build
	// valid CSRF cookies. Calling requireSessionCookie with a
	// forged env proves the defensive branch trips without
	// depending on the route table.
	env, err := mgr.Verify(forged)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/auth/sessions", nil)
	mw := authmw.New(storeAsAuthenticator(store), mgr, storeAsSessionLookup(store), nil, slog.New(slog.NewTextHandler(io.Discard, nil)), middleware.NewLimiter(middleware.AuthLimitConfig{}), nil)
	_, handled, err := mw.RequireSessionCookie(rec, req, env)
	if err != nil {
		t.Fatalf("requireSessionCookie returned err (should be silent handled=true): %v", err)
	}
	if !handled {
		t.Fatalf("handled = false, want true (request was rejected)")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if !problemHasCode(t, rec.Body.Bytes(), api.CodeSessionInvalid) {
		t.Errorf("body code = %s, want %s", rec.Body.String(), api.CodeSessionInvalid)
	}
	// Cookie must be cleared (Set-Cookie with MaxAge<0).
	if !cookieCleared(t, rec.Result().Cookies()) {
		t.Errorf("faas_sid cookie was not cleared after account-mismatch defensive trip")
	}
}
