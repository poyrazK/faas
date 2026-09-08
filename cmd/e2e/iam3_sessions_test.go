// iam3_sessions_test.go — IAM-3 (ADR-039, issue #187 + #244 merged)
// end-to-end sweep. Boots a real apid against a real PgStore, then
// drives the four /v1/auth/sessions* + /v1/auth/logout routes
// against an AEAD-bound session cookie whose sid stamps a real
// sessions row.
//
// This is the wire-shape acceptance proof for the IAM-3 design: the
// in-process handler test pins the apid-level behaviour
// (handlers_sessions_test.go); the memstore test pins the in-memory
// store semantics (pkg/state/memstore_test.go); this e2e proves
// the apid binary + the real pgx pool + the sqlc-generated
// statements + the AEAD cookie all line up end-to-end against
// Postgres.
//
// The test mints a fresh FAAS_SESSION_KEY and passes it to both
// the apid (via FAAS_SESSION_KEY env) and the test's own
// session.Manager so the AEAD signature round-trips. We don't run
// the magic-link verify path here because that needs a real email
// out (we'd need a stubbed mailer + token capture), and
// issueDashboardSession is the only place a session row gets
// stamped; verifying it once is enough to cover the wire.
//
// Subprocess-apid path (KVM-free, CI-safe). Requires Postgres; skip
// via FAAS_SKIP_PG_TESTS if the env doesn't have one.

package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestIAM3SessionsMatrixPg exercises the full IAM-3 wire surface
// against a real apid + a real pgx pool. Sibling-independence
// (the load-bearing reason #244 was chosen over #187) is the
// first assertion.
func TestIAM3SessionsMatrixPg(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)

	// Fresh session key shared between apid and the test's
	// session.Manager so the AEAD signature round-trips.
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		t.Fatalf("rand: %v", err)
	}
	keyHex := hex.EncodeToString(keyBytes)
	mgr, err := session.NewManager(keyBytes, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("session.NewManager: %v", err)
	}

	// Unique label so reruns against the same schema pick up
	// the same account (mirrors SeedAccount's duplicate-key
	// contract).
	const label = "iam3"
	email := "e2e+pro+" + label + "@test.example"
	acct, err := store.AccountByEmail(context.Background(), email)
	if err != nil {
		acct, err = store.CreateAccount(context.Background(), email, api.PlanPro)
		if err != nil {
			t.Fatalf("CreateAccount: %v", err)
		}
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_SESSION_KEY=" + keyHex,
	})
	client := h.HTTPClient()

	// Mint two sibling sessions for the same account. Each
	// creates a fresh row + a fresh cookie.
	cookieA, sidA := issueSessionForTest(t, store, mgr, acct.ID, "192.0.2.10", "iam3-A-ua")
	cookieB, sidB := issueSessionForTest(t, store, mgr, acct.ID, "203.0.113.20", "iam3-B-ua")

	t.Run("list_returns_both_with_current_flag", func(t *testing.T) {
		// (1) list from A's cookie — both rows present; A is current.
		raw, status := cookieList(t, client, h.APIDURL, cookieA)
		if status != http.StatusOK {
			t.Fatalf("list status = %d, want 200 (body=%s)", status, raw)
		}
		var resp api.SessionListResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Sessions) != 2 {
			t.Fatalf("count = %d, want 2", len(resp.Sessions))
		}
		var currentCount int
		for _, s := range resp.Sessions {
			if s.CurrentSession {
				currentCount++
			}
		}
		if currentCount != 1 {
			t.Errorf("CurrentSession count = %d, want exactly 1", currentCount)
		}
	})

	t.Run("revoke_sibling_blocks_sibling", func(t *testing.T) {
		// (2) A revokes B. 204. A still works. B's next request 401s.
		if status := cookieDelete(t, client, h.APIDURL, mgr, acct.ID, cookieA, "/v1/auth/sessions/"+sidB); status != http.StatusNoContent {
			t.Fatalf("revoke status = %d, want 204", status)
		}
		// A still works.
		if _, status := cookieList(t, client, h.APIDURL, cookieA); status != http.StatusOK {
			t.Errorf("post-revoke A status = %d, want 200", status)
		}
		// B is dead.
		raw, status := cookieList(t, client, h.APIDURL, cookieB)
		if status != http.StatusUnauthorized {
			t.Fatalf("post-revoke B status = %d, want 401 (body=%s)", status, raw)
		}
		if !problemHasCode(t, raw, api.CodeSessionExpired) {
			t.Errorf("B body code != session_expired (body=%s)", raw)
		}
	})

	t.Run("revoke_all_except_current", func(t *testing.T) {
		// (3) Mint a third sibling. Revoke-all-except-A. A still
		// works. The third sibling is dead.
		_, sidC := issueSessionForTest(t, store, mgr, acct.ID, "198.51.100.30", "iam3-C-ua")
		raw, status := cookiePost(t, client, h.APIDURL, mgr, acct.ID, cookieA, "/v1/auth/sessions/revoke_all", nil)
		if status != http.StatusOK {
			t.Fatalf("revoke_all status = %d, want 200 (body=%s)", status, raw)
		}
		var resp api.SessionsRevokeAllResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Revoked < 1 {
			t.Errorf("Revoked = %d, want >= 1 (sibling C is active)", resp.Revoked)
		}
		// C row should be revoked.
		c, err := store.GetSession(context.Background(), sidC)
		if err != nil {
			t.Fatalf("GetSession C: %v", err)
		}
		if c.RevokedAt == nil {
			t.Errorf("C row not revoked by revoke_all")
		}
		// A still works.
		if _, status := cookieList(t, client, h.APIDURL, cookieA); status != http.StatusOK {
			t.Errorf("post-revoke_all A status = %d, want 200", status)
		}
	})

	t.Run("logout_clears_cookie", func(t *testing.T) {
		// (4) Logout: 204, cookie cleared, row revoked.
		resp, err := cookiePostWithResp(t, client, h.APIDURL, mgr, acct.ID, cookieA, "/v1/auth/logout", nil)
		if err != nil {
			t.Fatalf("logout: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("logout status = %d, want 204", resp.StatusCode)
		}
		// Cookie is cleared.
		var cleared bool
		for _, c := range resp.Cookies() {
			if c.Name == "faas_sid" && (c.MaxAge < 0 || c.Value == "") {
				cleared = true
			}
		}
		if !cleared {
			t.Errorf("faas_sid cookie not cleared on logout")
		}
		// Row is revoked.
		row, err := store.GetSession(context.Background(), sidA)
		if err != nil {
			t.Fatalf("GetSession A: %v", err)
		}
		if row.RevokedAt == nil {
			t.Errorf("logout did not revoke A row")
		}
		// Next request: 401 + session_expired.
		raw, status := cookieList(t, client, h.APIDURL, cookieA)
		if status != http.StatusUnauthorized {
			t.Errorf("post-logout status = %d, want 401 (body=%s)", status, raw)
		}
		if !problemHasCode(t, raw, api.CodeSessionExpired) {
			t.Errorf("post-logout body code != session_expired (body=%s)", raw)
		}
	})

	t.Run("pre_iam3_cookie_rejected", func(t *testing.T) {
		// (5) An old (sid-less) cookie returns 401 with
		// CodeSessionExpired. The cookie is signed by the
		// shared manager (so Verify passes) but with sid=""
		// (the pre-IAM-3 IssueWithMFAFlag(id, false) shape);
		// requireSessionCookie clears it and returns the
		// rollout invalidation code. This is the load-bearing
		// acceptance for the rollout.
		preCookie, err := mgr.IssueWithMFAFlag(acct.ID, false)
		if err != nil {
			t.Fatalf("IssueWithMFAFlag: %v", err)
		}
		raw, status := cookieList(t, client, h.APIDURL,
			&http.Cookie{Name: "faas_sid", Value: preCookie})
		if status != http.StatusUnauthorized {
			t.Fatalf("pre-IAM-3 status = %d, want 401 (body=%s)", status, raw)
		}
		if !problemHasCode(t, raw, api.CodeSessionExpired) {
			t.Errorf("pre-IAM-3 code != session_expired (body=%s)", raw)
		}
	})

	t.Run("bearer_key_unaffected", func(t *testing.T) {
		// (6) Bearer API keys never touch sessions — same wire
		// code, no GetSession lookup. We seed a key via the
		// store (mirrors SeedAccount's contract) and confirm
		// /v1/account returns 200.
		pt, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatalf("GenerateAPIKey: %v", err)
		}
		if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash,
			"iam3-bearer", api.ScopesAdminOnly); err != nil {
			t.Fatalf("CreateAPIKey: %v", err)
		}
		req, _ := http.NewRequest(http.MethodGet, h.APIDURL+"/v1/account", nil)
		req.Header.Set("Authorization", "Bearer "+pt)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("bearer /v1/account: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("bearer /v1/account status = %d, want 200 (body=%s)", resp.StatusCode, raw)
		}
		// No new sessions row was created by the bearer request.
		// We seeded only the two siblings above; ListSessions
		// should not have grown.
		rows, err := store.ListSessions(context.Background(), acct.ID)
		if err != nil {
			t.Fatalf("ListSessions: %v", err)
		}
		// After the earlier subtests, A is revoked, B is
		// revoked, C is revoked. So 0 active rows remain.
		for _, r := range rows {
			if r.RevokedAt == nil {
				t.Errorf("bearer request created session row %s", r.ID)
			}
		}
	})
}

// issueSessionForTest stamps a sessions row + AEAD-seals a cookie
// exactly the way issueDashboardSession does in production. We
// inline it here so the test stays decoupled from the apid
// helper's package-private visibility.
func issueSessionForTest(t *testing.T, store *state.PgStore, mgr *session.Manager, accountID, ip, ua string) (*http.Cookie, string) {
	t.Helper()
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, accountID, ip, ua); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cookieVal, err := mgr.IssueWithSession(sid, accountID, false)
	if err != nil {
		t.Fatalf("IssueWithSession: %v", err)
	}
	return &http.Cookie{Name: "faas_sid", Value: cookieVal}, sid
}

// cookieList runs GET /v1/auth/sessions with the given cookie.
func cookieList(t *testing.T, c *http.Client, base string, cookie *http.Cookie) ([]byte, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/auth/sessions", nil)
	req.AddCookie(cookie)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode
}

// csrfActionForPath mirrors the IAM-3 CSRF action names in
// handlers_sessions.go so the e2e can mint matching tokens. GET
// routes aren't CSRF-gated; only the three mutating routes are.
func csrfActionForPath(method, path string) string {
	if method == http.MethodDelete && strings.HasPrefix(path, "/v1/auth/sessions/") &&
		!strings.HasSuffix(path, "/revoke_all") {
		return "auth.session.revoke"
	}
	if method == http.MethodPost && strings.HasSuffix(path, "/v1/auth/sessions/revoke_all") {
		return "auth.sessions.revoke_all"
	}
	if method == http.MethodPost && strings.HasSuffix(path, "/v1/auth/logout") {
		return "auth.logout"
	}
	return ""
}

// injectCSRFCookieAndField adds the faas_csrf cookie (carrying the
// AEAD-bound token) and a `csrf_token` JSON-body field with the
// same opaque value, matching the CSRF verify path in
// pkg/middleware.VerifyAuthenticated (form-or-JSON body transport).
// Mutates req.Body + ContentLength so the JSON decoder downstream
// still sees the original payload.
func injectCSRFCookieAndField(req *http.Request, tok string) {
	req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
	body := []byte(`{"csrf_token":"` + tok + `"}`)
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
}

// withCSRFIfNeeded mints + injects a CSRF token for mutating
// routes. No-op for GET paths (and any path not gated by
// csrfActionForPath).
func withCSRFIfNeeded(t *testing.T, c *http.Client, base string, mgr *session.Manager, accountID, method, path string, req *http.Request) {
	t.Helper()
	action := csrfActionForPath(method, path)
	if action == "" {
		return
	}
	tok, err := middleware.IssueForAuthenticated(mgr, action, accountID)
	if err != nil {
		t.Fatalf("issue CSRF for action %q: %v", action, err)
	}
	injectCSRFCookieAndField(req, tok)
}

// cookieDelete runs DELETE /v1/auth/sessions/{id} with the given
// cookie. Returns the status code.
func cookieDelete(t *testing.T, c *http.Client, base string, mgr *session.Manager, accountID string, cookie *http.Cookie, path string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, base+path, nil)
	req.AddCookie(cookie)
	withCSRFIfNeeded(t, c, base, mgr, accountID, http.MethodDelete, path, req)
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("delete %s: %v", path, err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	return resp.StatusCode
}

// cookiePost runs POST path with the given cookie + nil body.
func cookiePost(t *testing.T, c *http.Client, base string, mgr *session.Manager, accountID string, cookie *http.Cookie, path string, body any) ([]byte, int) {
	t.Helper()
	resp, err := cookiePostWithResp(t, c, base, mgr, accountID, cookie, path, body)
	if err != nil {
		t.Fatalf("post %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return raw, resp.StatusCode
}

// cookiePostWithResp returns the full response so the caller can
// inspect Set-Cookie headers (logout clears the cookie).
func cookiePostWithResp(t *testing.T, c *http.Client, base string, mgr *session.Manager, accountID string, cookie *http.Cookie, path string, body any) (*http.Response, error) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(http.MethodPost, base+path, r)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(cookie)
	withCSRFIfNeeded(t, c, base, mgr, accountID, http.MethodPost, path, req)
	return c.Do(req)
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

// bytesReader + readCloser are tiny stdio helpers so this file
// doesn't pull in "bytes" and "strings" solely for the POST body.
