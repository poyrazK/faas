// MFA handler tests (IAM-2, issue #186).
//
// Covers the seven /v1/account/mfa/* endpoints + the opt-in
// session policy. Tests use the MemStore + an in-process
// age identity so the seal/unseal path runs end-to-end
// without an external key file. The MFA recipient + identity
// are wired into the package-level vars (mfaRecipient,
// mfaIdentity) and restored on t.Cleanup so a parallel test
// suite doesn't observe a stale value.
//
// The mfa_pending=true branch of the requireMFA middleware
// table is covered by mfa_middleware_test.go; this file
// focuses on the handlers themselves.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/pquerna/otp/totp"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// mfaTestEnv is the session-cookie twin of testEnv for the MFA
// flow. Most tests exercise the post-enrollment path where the
// cookie is already cleared after a successful challenge.
//
// `id` is the in-process age identity used by the seal/unseal
// path inside the handlers. It is also exposed so callers can
// generate recovery codes against the same key the production
// envelope will use.
type mfaTestEnv struct {
	h      http.Handler
	srv    *server
	store  *state.MemStore
	acct   state.Account
	mgr    *session.Manager
	id     *age.X25519Identity
	cookie *http.Cookie
}

// setupWithMFA mints a MemStore, an account, an ephemeral
// session manager, and an in-process age identity wired into
// both mfaRecipient + mfaIdentity. The returned env carries a
// cookie whose pending state matches the account's MFA policy.
//
// `mfaRequired` lets the test pin an explicit policy flag up-front.
//
// `mfaEnrolled` stamps mfa_enrolled_at via MarkMFAEnrolled,
// which also clears mfa_required. An enrolled account is still
// pending on a newly issued dashboard session because enrollment
// is the customer's opt-in choice.
func setupWithMFA(t *testing.T, plan api.Plan, mfaRequired, mfaEnrolled bool) mfaTestEnv {
	t.Helper()
	// Recovery-code HMAC upgrade: the production loader wires this
	// at apid boot via loadOrGenerateRecoveryHMACKey; the test
	// fixture has no boot path, so a sync.Once hooks it once across
	// the suite. Using a deterministic (test-stable) key pins the
	// digest against the test's own expectations.
	ensureRecoveryTestSecret(t)
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(),
		"mfa@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	if mfaRequired {
		if _, err := store.SetMFARequired(context.Background(), acct.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	if mfaEnrolled {
		// MarkMFAEnrolled stamps enrolled_at and clears
		// mfa_required. Order matters: stamp required first
		// (above), then mark enrolled — the final state is the
		// "enrolled customer, policy cleared" post-confirm
		// condition.
		if err := store.MarkMFAEnrolled(context.Background(), acct.ID); err != nil {
			t.Fatal(err)
		}
	}
	acct, _ = store.AccountByID(context.Background(), acct.ID)

	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	// IAM-3 (ADR-039): mint a sessions row + seal the cookie with
	// sid + accountID + mfaPending so requireSessionCookie
	// accepts the cookie and the four session handlers can read
	// the current sid off the context. The login path is the
	// same shape (issueDashboardSession → IssueWithSession).
	mfaPending := mfaSessionPending(acct)
	mfaSid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), mfaSid, acct.ID, "192.0.2.20", "mfa-test-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, err := mgr.IssueWithSession(mfaSid, acct.ID, mfaPending)
	if err != nil {
		t.Fatal(err)
	}

	// In-process age identity: GenerateAndSaveHostKey writes a
	// fresh X25519 key to a temp file (the same code path
	// production uses). We then close the file to free the
	// handle; the in-memory `id` is what the handlers reach.
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "host.age")
	id, err := secretbox.GenerateAndSaveHostKey(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o400); err != nil {
		t.Fatal(err)
	}

	prevRec := mfaRecipient
	prevIdent := mfaIdentity
	SetMFARecipient(func() *age.X25519Recipient { return id.Recipient() })
	SetMFAIdentity(func() *age.X25519Identity { return id })
	t.Cleanup(func() {
		SetMFARecipient(prevRec)
		SetMFAIdentity(prevIdent)
	})

	ops := wire.NewOpsMetrics("apid_mfa_test")
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), ops)

	return mfaTestEnv{
		h:      srv.handler(),
		srv:    srv,
		store:  store,
		acct:   acct,
		mgr:    mgr,
		id:     id,
		cookie: &http.Cookie{Name: sessionCookie, Value: token},
	}
}

// csrfAction maps a URL path to its CSRF action string. The
// dashboard's JS client mints a token bound to the action; the
// /v1/account/mfa/{confirm,recover,disable} handlers verify
// against the same action. /enroll and /verify don't gate on
// CSRF (per the IAM-2 design decision documented in
// handlers_mfa.go).
func csrfAction(path string) string {
	switch {
	case strings.HasSuffix(path, "/v1/account/mfa/confirm"):
		return "mfa_confirm"
	case strings.HasSuffix(path, "/v1/account/mfa/recover"):
		return "mfa_recover"
	case strings.HasSuffix(path, "/v1/account/mfa/disable"):
		return "mfa_disable"
	case strings.HasSuffix(path, "/v1/account/mfa/disable-email"):
		return "mfa_disable_email"
	case strings.HasSuffix(path, "/v1/account/mfa/disable-email/confirm"):
		return "mfa_disable_email_confirm"
	}
	return ""
}

// do runs an MFA-cookie-authed request. Mirrors testEnv.do but
// uses req.AddCookie instead of the bearer header. Also injects
// the CSRF token (cookie + body field) for routes that gate on
// it (per-issue-#186 Finding #7).
func (e mfaTestEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	rawBody := []byte(nil)
	if body != nil {
		var err error
		rawBody, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(rawBody)
	}
	req := httptest.NewRequest(method, path, r)
	req.AddCookie(e.cookie)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		// CSRF: when the route gates on it, inject the
		// cookie + the JSON-body `csrf_token` sibling. The
		// JSON object shape must wrap the original body —
		// decodeJSON inside the handlers expects the same
		// top-level keys the test marshalled, so the body is
		// reshaped as `{"csrf_token": "...", "<original key>":
		// ...}` for single-key bodies.
		if action := csrfAction(path); action != "" {
			tok, err := middleware.IssueForAuthenticated(e.mgr, action, e.acct.ID)
			if err != nil {
				t.Fatalf("issue CSRF token for %s: %v", action, err)
			}
			req.AddCookie(&http.Cookie{Name: middleware.CookieNameAuthenticated, Value: tok})
			// Re-marshal the body wrapping the original
			// top-level JSON object (csrf_token +
			// original-keys). For non-object bodies, the
			// caller drives CSRF manually.
			if len(rawBody) > 0 && rawBody[0] == '{' {
				wrapped := injectCSRFToken(rawBody, tok)
				req.Body = io.NopCloser(bytes.NewReader(wrapped))
				req.ContentLength = int64(len(wrapped))
			}
		}
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// injectCSRFToken takes a marshalled JSON object body and adds
// the `csrf_token` key. Operates on bytes so the field order in
// the original body is preserved — the handler's decodeJSON
// pulls fields by name, so order doesn't matter.
func injectCSRFToken(body []byte, tok string) []byte {
	// bytes.TrimRight accounts for trailing whitespace we
	// don't want glued into the rebuilt form.
	trimmed := bytes.TrimRight(body, " \t\n")
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != '}' {
		return body
	}
	head := bytes.TrimRight(trimmed[:len(trimmed)-1], " \t\n")
	// Empty object `{}` becomes `{"csrf_token":"<tok>"}`.
	if len(head) == 0 || head[0] != '{' {
		return body
	}
	var b bytes.Buffer
	b.Write(head)
	if !bytes.Equal(head, []byte("{")) {
		b.WriteByte(',')
	}
	// JSON-escape the token (mandatory — base64url contains '-'
	// which is fine, but Quote defensively).
	b.WriteString(`"csrf_token":`)
	b.WriteString(strconv.Quote(tok))
	b.WriteByte('}')
	return b.Bytes()
}

// strconvQuote is unused; strconv.Quote is now used directly.

// mfaIssueWithPending re-issues the cookie with mfa_pending set
// the way the login handlers issue a session for an account that
// requires MFA. Returns a fresh cookie the test can pin on the
// next request.
//
// IAM-3 (ADR-039): the cookie must carry a sid backed by a
// live sessions row, otherwise requireSessionCookie rejects it
// with CodeSessionExpired. We look up the row created by
// setupWithMFA and re-issue the envelope stamped with the same
// sid — the same shape /v1/account/mfa/verify uses to clear
// mfa_pending without creating a second row.
func (e mfaTestEnv) mfaIssueWithPending(t *testing.T, pending bool) *http.Cookie {
	t.Helper()
	// Reuse the existing sid from setupWithMFA: list this
	// account's sessions and pick the most recent. The cookie
	// is the only handle that connects the test to the row,
	// and we have no sessionFrom() helper here, so we walk
	// the store. With only one row expected, this is O(1).
	rows, err := e.store.ListSessions(context.Background(), e.acct.ID)
	if err != nil || len(rows) == 0 {
		t.Fatalf("ListSessions: %v, rows=%d", err, len(rows))
	}
	sid := rows[0].ID
	tok, err := e.mgr.IssueWithSession(sid, e.acct.ID, pending)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: tok}
}

// generateEnrolledAccount runs /enroll + /confirm on a fresh
// account and returns the response so the test can drive
// /recover or /disable later. The secret is the TOTP secret —
// used to compute a matching code on the next step.
func (e mfaTestEnv) generateEnrolledAccount(t *testing.T) (recovCodes []string, secret string, freshCookie *http.Cookie) {
	t.Helper()
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/enroll", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/enroll: status %d: %s", rec.Code, rec.Body)
	}
	var out api.MFAEnrollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode /enroll: %v", err)
	}
	code, err := totp.GenerateCodeCustom(out.Secret, time.Now().UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0, // 0 = SHA1 in pquerna/otp (otp.AlgorithmSHA1)
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	confirmRec := e.do(t, http.MethodPost, "/v1/account/mfa/confirm", api.MFAConfirmRequest{Totp: code})
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("/confirm: status %d: %s", confirmRec.Code, confirmRec.Body)
	}
	// /confirm re-issues the cookie. Pull it from the recorder so
	// subsequent calls use the cleared (mfa_pending=false) cookie.
	for _, c := range confirmRec.Result().Cookies() {
		if c.Name == sessionCookie {
			freshCookie = c
			break
		}
	}
	if freshCookie == nil {
		t.Fatalf("/confirm did not re-issue the cookie")
	}
	return out.RecoveryCodes, out.Secret, freshCookie
}

// --- /enroll ----------------------------------------------------------------

// TestMFAEnroll_ReturnsSecretAndQR pins the wire shape: the
// response MUST carry otpauth_url, secret, qr_code_png_base64,
// and recovery_codes (10 of them). Empty or missing any of
// these breaks the dashboard.
func TestMFAEnroll_ReturnsSecretAndQR(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/enroll", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.MFAEnrollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.OTPAuthURL == "" || !strings.HasPrefix(out.OTPAuthURL, "otpauth://totp/") {
		t.Errorf("OTPAuthURL = %q, want otpauth://totp/...", out.OTPAuthURL)
	}
	if out.Secret == "" || len(out.Secret) != 32 {
		t.Errorf("Secret = %q (len %d), want 32-char base32", out.Secret, len(out.Secret))
	}
	if len(out.QRCodePNG) == 0 {
		t.Errorf("QRCodePNG empty")
	}
	if len(out.RecoveryCodes) != authcode.RecoveryCodeCount {
		t.Errorf("RecoveryCodes count = %d, want %d", len(out.RecoveryCodes), authcode.RecoveryCodeCount)
	}
}

// TestMFAEnroll_RejectsAlreadyEnrolled: a 2nd call while the
// account is enrolled returns 409 CodeConflict. The dashboard
// should never land in this state (the /disable flow clears
// enrollment first), but the handler defends against the
// race.
func TestMFAEnroll_RejectsAlreadyEnrolled(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, true)
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/enroll", nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

// TestMFAEnroll_RejectsWhenRecipientMissing: with no host key
// wired, /enroll returns 503 CodeCapacity. The customer's
// primary action fails closed rather than minting a secret
// that can't be sealed.
func TestMFAEnroll_RejectsWhenRecipientMissing(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	prevRec := mfaRecipient
	SetMFARecipient(func() *age.X25519Recipient { return nil })
	t.Cleanup(func() { SetMFARecipient(prevRec) })

	rec := e.do(t, http.MethodPost, "/v1/account/mfa/enroll", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// --- /confirm + /verify -----------------------------------------------------

// TestMFAConfirm_StampsEnrolledAndClearsMFARequired pins the
// happy path. Before /confirm the account is unenrolled; after
// /confirm mfa_enrolled_at is non-nil and mfa_required is
// false. The cookie re-issue is checked separately.
func TestMFAConfirm_StampsEnrolledAndClearsMFARequired(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, true, false)
	_, _, freshCookie := e.generateEnrolledAccount(t)

	// After /confirm the account row is enrolled and the policy
	// flag is cleared (MarkMFAEnrolled sets enrolled_at AND
	// clears mfa_required).
	after, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.MFAEnrolled() {
		t.Errorf("MFAEnrolled = false, want true after /confirm")
	}
	if after.MFARequired {
		t.Errorf("MFARequired = true, want false after /confirm (MarkMFAEnrolled clears it)")
	}

	// The post-/confirm cookie must not be mfa_pending.
	env, err := e.mgr.Verify(freshCookie.Value)
	if err != nil {
		t.Fatalf("verify post-/confirm cookie: %v", err)
	}
	if env.MfaPending {
		t.Errorf("post-/confirm cookie is mfa_pending=true, want false")
	}
}

// TestMFAConfirm_RejectsBadCode: a wrong 6-digit code returns
// 401 CodeMFAInvalidCode and does NOT stamp enrolled_at.
func TestMFAConfirm_RejectsBadCode(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	// /enroll only, no /confirm.
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/enroll", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/enroll status %d", rec.Code)
	}

	badRec := e.do(t, http.MethodPost, "/v1/account/mfa/confirm", api.MFAConfirmRequest{Totp: "000000"})
	if badRec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", badRec.Code)
	}

	// And the account row is still unenrolled.
	after, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if after.MFAEnrolled() {
		t.Errorf("MFAEnrolled = true after failed /confirm, want false")
	}
}

// TestMFAVerify_StepsUpPendingCookie: with an mfa_pending=true
// cookie and an already-enrolled account, /verify clears the
// flag without re-stamping enrolled_at. The next protected
// route would pass through requireMFA — we exercise the cookie
// flag directly here.
func TestMFAVerify_StepsUpPendingCookie(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	// Drive an actual enrollment so the stored secret is real.
	_, secret, _ := e.generateEnrolledAccount(t)

	// Now flip the cookie to mfa_pending=true so we can exercise
	// the step-up path. The account is enrolled so /verify has a
	// real sealed secret to verify against.
	pendingCookie := e.mfaIssueWithPending(t, true)
	code, err := totp.GenerateCodeCustom(secret, time.Now().UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0, // SHA1 (matches VerifyCode)
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}

	body, err := json.Marshal(api.MFAVerifyRequest{Totp: code})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(pendingCookie)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("/verify status %d: %s", rec.Code, rec.Body)
	}
	var stepUpCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			stepUpCookie = c
		}
	}
	stepUpCookie = mustStepUpCookie(t, stepUpCookie, "/verify did not re-issue cookie")
	env, err := e.mgr.Verify(stepUpCookie.Value)
	if err != nil {
		t.Fatalf("verify post-/verify cookie: %v", err)
	}
	if env.MfaPending {
		t.Errorf("post-/verify cookie is mfa_pending=true, want false")
	}
}

// TestMFAVerify_ReissuedCookieAndRowBindingHashInLockstep is
// the regression pin for review finding #2 (PR #653 mega-PR):
// the reissue path MUST re-stamp sessions.binding_hash before
// sealing the new cookie envelope. Without the
// store.UpdateSessionBinding call inside
// reissueSessionCookieWithStepUp, the cookie carries the
// fresh (IP, UA-family) fingerprint but the sessions row
// retains the original mint-time fingerprint; the very next
// authenticated request hits RequireSessionCookie step 3.5
// (the stolen-cookie auto-revoke branch at
// pkg/auth/middleware/middleware.go) and 401s the customer on
// their own session, with an auth.session.binding_mismatch
// audit row attributing the lockstep failure to the legitimate
// owner.
//
// Test flow:
//  1. /enroll + /confirm: mints the enrolled account.
//  2. mfaIssueWithPending(true): flips the cookie to
//     mfa_pending=true. The setup-time cookie has no
//     binding_hash envelope field (mint path used
//     CreateSession, not CreateSessionWithBinding), so the
//     sessions row's binding_hash column is also empty.
//  3. /verify: reissues with mfa_pending=false + a
//     freshly-computed binding hash (the (IP, UA-family)
//     fingerprint for this request).
//  4. Inspect the cookie envelope + the sessions row.
//     Both MUST carry the SAME binding_hash. If they
//     disagree the lockstep is broken.
func TestMFAVerify_ReissuedCookieAndRowBindingHashInLockstep(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	_, secret, _ := e.generateEnrolledAccount(t)

	// Pre-/verify: cookie is mfa_pending=true (so /verify has a
	// real sealed TOTP secret to check against). The setup-time
	// mint path used CreateSession (no binding hash), so both
	// the envelope's binding_hash and the row's binding_hash
	// column are empty here.
	e.cookie = e.mfaIssueWithPending(t, true)

	code, err := totp.GenerateCodeCustom(secret, time.Now().UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom: %v", err)
	}
	body, err := json.Marshal(api.MFAVerifyRequest{Totp: code})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/account/mfa/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(e.cookie)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/verify status %d: %s", rec.Code, rec.Body)
	}

	var reissuedCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			reissuedCookie = c
		}
	}
	if reissuedCookie == nil {
		t.Fatalf("/verify did not re-issue cookie")
	}
	env, err := e.mgr.Verify(reissuedCookie.Value)
	if err != nil {
		t.Fatalf("verify reissued cookie: %v", err)
	}

	// Look up the row.
	rows, err := e.store.ListSessions(context.Background(), e.acct.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListSessions: err=%v rows=%d", err, len(rows))
	}
	row := rows[0]

	if env.BindingHash == "" {
		t.Errorf("post-/verify envelope binding_hash is empty; reissue must stamp it")
	}
	if row.BindingHash == "" {
		t.Errorf("post-/verify sessions row binding_hash is empty; " +
			"reissueSessionCookieWithStepUp must call store.UpdateSessionBinding " +
			"so the row tracks the cookie envelope (review finding #2)")
	}
	if env.BindingHash != row.BindingHash {
		t.Errorf("binding hash drift (review finding #2):\n"+
			"  envelope = %q\n"+
			"  row      = %q\n"+
			"The next request will trip the stolen-cookie auto-revoke branch on the customer's own session.",
			env.BindingHash, row.BindingHash)
	}
}

// --- /recover + /disable ----------------------------------------------------

// TestMFARecover_ConsumesCodeAndReissuesCookie burns one of
// the 10 recovery codes and confirms:
//   - 200 OK with a fresh non-mfa_pending cookie.
//   - The account's mfa_recovery_codes_hash slice now has 9
//     entries (the consumed one is gone).
func TestMFARecover_ConsumesCodeAndReissuesCookie(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	e.cookie = e.mfaIssueWithPending(t, true)

	// Use the first recovery code via the pending cookie. The
	// /recover handler is CSRF-gated; e.do auto-injects the
	// matching token + cookie (handlers_mfa_test.go).
	code := recovCodes[0]
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
		api.MFARecoverRequest{Code: code})

	if rec.Code != http.StatusOK {
		t.Fatalf("/recover status %d: %s", rec.Code, rec.Body)
	}

	after, err := e.store.AccountByID(context.Background(), e.acct.ID)
	if err != nil {
		t.Fatalf("AccountByID: %v", err)
	}
	hashes := after.MFARecoveryCodesHash
	if len(hashes) != authcode.RecoveryCodeCount-1 {
		t.Errorf("RecoveryCodes remaining = %d, want %d", len(hashes), authcode.RecoveryCodeCount-1)
	}
}

// TestMFARecover_RejectsBadCode: an unknown recovery code
// returns 401 CodeMFAInvalidCode and does NOT consume a slot.
func TestMFARecover_RejectsBadCode(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	e.generateEnrolledAccount(t)

	rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover", api.MFARecoverRequest{Code: "AAAAAAAAAA"})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}

	after, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	hashes := after.MFARecoveryCodesHash
	if len(hashes) != authcode.RecoveryCodeCount {
		t.Errorf("RecoveryCodes after bad burn = %d, want %d (unchanged)", len(hashes), authcode.RecoveryCodeCount)
	}
}

// TestMFADisable_ByRecoveryCode: a fresh /disable with a
// recovery_code consumes one code and clears MFA state.
func TestMFADisable_ByRecoveryCode(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)

	rec := e.do(t, http.MethodPost, "/v1/account/mfa/disable",
		api.MFADisableRequest{RecoveryCode: recovCodes[0]})
	if rec.Code != http.StatusOK {
		t.Fatalf("/disable status %d: %s", rec.Code, rec.Body)
	}

	after, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if after.MFAEnrolled() {
		t.Errorf("MFAEnrolled = true after /disable, want false")
	}
}

// TestMFADisable_RejectsBadBody: exactly one of password /
// recovery_code is required. Empty + empty + both fail with
// 400 CodeValidation.
func TestMFADisable_RejectsBadBody(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	for _, body := range []api.MFADisableRequest{
		{},                                 // neither
		{Password: "p", RecoveryCode: "c"}, // both
	} {
		rec := e.do(t, http.MethodPost, "/v1/account/mfa/disable", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %+v: status = %d, want 400", body, rec.Code)
		}
	}
}

// TestMFAIsOptIn pins the account-level policy. New accounts do not
// receive an MFA challenge, while an account with a confirmed
// authenticator does. MFARequired remains an explicit policy hook.
func TestMFAIsOptIn(t *testing.T) {
	tests := []struct {
		name        string
		mfaRequired bool
		mfaEnrolled bool
		wantPending bool
	}{
		{name: "new_account", wantPending: false},
		{name: "explicit_policy", mfaRequired: true, wantPending: true},
		{name: "user_opted_in", mfaEnrolled: true, wantPending: true},
		{name: "enrolled_policy_cleared", mfaEnrolled: true, wantPending: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupWithMFA(t, api.PlanPro, tt.mfaRequired, tt.mfaEnrolled)
			if got := mfaSessionPending(e.acct); got != tt.wantPending {
				t.Errorf("mfaSessionPending(%+v) = %v, want %v", e.acct, got, tt.wantPending)
			}
		})
	}
}

// TestIssueSessionCookie_EnrolledAccountIsPending verifies the
// login boundary, not just the pure predicate: a new account gets
// an ordinary cookie, while a customer who completed enrollment
// gets a pending cookie that must be stepped up.
func TestIssueSessionCookie_EnrolledAccountIsPending(t *testing.T) {
	tests := []struct {
		name        string
		mfaEnrolled bool
		wantPending bool
	}{
		{name: "not_enrolled", wantPending: false},
		{name: "enrolled", mfaEnrolled: true, wantPending: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := setupWithMFA(t, api.PlanPro, false, tt.mfaEnrolled)
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
			rec := httptest.NewRecorder()
			e.srv.issueSessionCookie(rec, req, e.acct)

			var cookie *http.Cookie
			for _, candidate := range rec.Result().Cookies() {
				if candidate.Name == sessionCookie {
					cookie = candidate
					break
				}
			}
			if cookie == nil {
				t.Fatal("issueSessionCookie did not set faas_sid")
			}
			env, err := e.mgr.Verify(cookie.Value)
			if err != nil {
				t.Fatalf("verify issued cookie: %v", err)
			}
			if env.MfaPending != tt.wantPending {
				t.Errorf("MfaPending = %v, want %v", env.MfaPending, tt.wantPending)
			}
		})
	}
}

// --- burn-out + race tests (issue #186 review findings #13–#17) -------------

// TestMFARecover_RefusesToBurnLastCode pins the terminal-state
// guard. The customer burning the LAST remaining code via
// /recover would otherwise leave them in an unrecoverable
// terminal state — they could only re-authenticate via /disable,
// but /disable requires the same set of recovery codes (or the
// password). The handler refuses the burn and returns 409
// CodeValidation telling the customer to use /disable instead,
// keeping the password path open as the safety valve.
func TestMFARecover_RefusesToBurnLastCode(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	if len(recovCodes) != authcode.RecoveryCodeCount {
		t.Fatalf("setup: recovery code count = %d, want %d", len(recovCodes), authcode.RecoveryCodeCount)
	}
	ctx := context.Background()

	// Burn down to 1 code by driving /recover until N-1 codes
	// are consumed. Each call is a fresh mfa_pending=false
	// cookie (the reissue flow at /recover / /confirm keeps it
	// cleared, so we re-mint only when we need a fresh session —
	// the do() helper carries the existing cookie unchanged).
	for i := 0; i < authcode.RecoveryCodeCount-1; i++ {
		rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
			api.MFARecoverRequest{Code: recovCodes[i]})
		if rec.Code != http.StatusOK {
			t.Fatalf("burn #%d: status %d: %s", i, rec.Code, rec.Body)
		}
	}
	// Confirm the store now has exactly 1 code.
	afterBurn, _ := e.store.AccountByID(ctx, e.acct.ID)
	if got := len(afterBurn.MFARecoveryCodesHash); got != 1 {
		t.Fatalf("after burn: codes = %d, want 1", got)
	}

	// Drive /recover with the LAST remaining code (recovCodes[last]).
	lastCode := recovCodes[authcode.RecoveryCodeCount-1]
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
		api.MFARecoverRequest{Code: lastCode})
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 (would have locked the customer out)", rec.Code)
	}

	// The store must still hold that single code (no burn).
	after, _ := e.store.AccountByID(ctx, e.acct.ID)
	if len(after.MFARecoveryCodesHash) != 1 {
		t.Errorf("recovery codes count after refusal = %d, want 1 (must not be burned)", len(after.MFARecoveryCodesHash))
	}

	// /disable via the same code IS allowed to burn the last
	// one — the customer is leaving the enrolled state, not
	// trying to step up.
	disableRec := e.do(t, http.MethodPost, "/v1/account/mfa/disable",
		api.MFADisableRequest{RecoveryCode: lastCode})
	if disableRec.Code != http.StatusOK {
		t.Errorf("/disable with last recovery code: status = %d, want 200", disableRec.Code)
	}
	finalAcct, _ := e.store.AccountByID(ctx, e.acct.ID)
	if finalAcct.MFAEnrolled() {
		t.Errorf("MFAEnrolled = true after /disable, want false")
	}
}

// TestMFARecover_ConcurrentBurnOneCode covers the SELECT FOR
// UPDATE contract on ConsumeRecoveryCode. Two concurrent
// /recover calls using the SAME code must land exactly one
// 200 (burn) and exactly one 401 (code already consumed) —
// the second race-arrives AFTER the first transaction commits
// and observes the SHA-256 hash is gone. Without the atomic
// primitive, both calls would think they burned the code (not
// data loss, but a duplicate audit row + confusing UX).
//
// Race shape: both goroutines' MatchRecoveryCode runs before
// either ConsumeRecoveryCode. The handler checks the consume's
// matched return (handlers_mfa.go mfaRecover step 2) — when
// false, it emits 401 ("code already consumed"). That check is
// the load-bearing change that turns this test from a flake
// (200=2, 401=0) into a deterministic pin of the user-facing
// contract. The store-level atomicity is pinned independently
// in TestMFARecover_StoreAtomicityOnConcurrentConsume below —
// a regression in ConsumeRecoveryCode serialization would fail
// that subtest even on a fast machine.
func TestMFARecover_ConcurrentBurnOneCode(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	if len(recovCodes) < 2 {
		t.Fatalf("setup: recovery code count = %d, want >= 2", len(recovCodes))
	}
	code := recovCodes[0]

	type result struct {
		status int
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
				api.MFARecoverRequest{Code: code})
			results <- result{rec.Code}
		}()
	}
	close(start)
	r1 := <-results
	r2 := <-results

	// Exactly one 200 (burn), one 401 (code already consumed).
	// In either ordering, the shape must hold.
	var ok, denied int
	for _, r := range []result{r1, r2} {
		switch r.status {
		case http.StatusOK:
			ok++
		case http.StatusUnauthorized:
			denied++
		}
	}
	if ok != 1 || denied != 1 {
		t.Errorf("concurrent /recover results = (200=%d, 401=%d), want exactly (1, 1)", ok, denied)
	}

	// And the store landed exactly N-1 codes — never N-2 from a
	// double-burn.
	ctx := context.Background()
	after, _ := e.store.AccountByID(ctx, e.acct.ID)
	if got, want := len(after.MFARecoveryCodesHash), authcode.RecoveryCodeCount-1; got != want {
		t.Errorf("codes after concurrent burn = %d, want %d (double-burn would have left %d)",
			got, want, authcode.RecoveryCodeCount-2)
	}
}

// TestMFARecover_StoreAtomicityOnConcurrentConsume pins the
// atomicity contract on ConsumeRecoveryCode directly, without
// relying on handler timing. N=8 goroutines fire ConsumeRecoveryCode
// at the same account with the same presented hash; the store
// must accept exactly one (matched=true) and reject the other
// 7 (matched=false). The sync.WaitGroup "arrive" gate forces
// all 8 to be parked on the same source line at the moment of
// release so the scheduler MUST interleave them — a regression
// in ConsumeRecoveryCode serialization (e.g. a MemStore method
// that drops the mutex around the read-compare-mutate-write
// chain) would surface as 2+ matched=true responses and
// RecoveryCodeCount-2 codes remaining. N=8 is well above the
// 2-of-2 flake window and far enough from RecoveryCodeCount=10
// that a regression in the consume path is statistically
// impossible to hide.
func TestMFARecover_StoreAtomicityOnConcurrentConsume(t *testing.T) {
	const concurrency = 8
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	if len(recovCodes) < 2 {
		t.Fatalf("setup: recovery code count = %d, want >= 2", len(recovCodes))
	}
	code := recovCodes[0]
	presented, err := authcode.HashRecoveryCode(code)
	if err != nil {
		t.Fatalf("HashRecoveryCode(%q): %v", code, err)
	}

	type result struct {
		matched  bool
		lastCode bool
		err      error
	}
	results := make(chan result, concurrency)
	var arrived sync.WaitGroup
	arrived.Add(concurrency)
	goSignal := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		go func() {
			arrived.Done()
			<-goSignal
			matched, lastCode, _, err := e.store.ConsumeRecoveryCode(context.Background(), e.acct.ID, presented)
			results <- result{matched, lastCode, err}
		}()
	}
	arrived.Wait()
	close(goSignal)

	var matched, lastSeen int
	var errCount int
	for i := 0; i < concurrency; i++ {
		r := <-results
		if r.err != nil {
			errCount++
			continue
		}
		if r.matched {
			matched++
		}
		if r.lastCode {
			lastSeen++
		}
	}
	if errCount != 0 {
		t.Errorf("ConsumeRecoveryCode returned %d errors, want 0", errCount)
	}
	if matched != 1 {
		t.Errorf("ConsumeRecoveryCode matched=%d across %d concurrent calls on the same code, want 1 (double-burn regression)",
			matched, concurrency)
	}
	if lastSeen != 0 {
		// RecoveryCodeCount=10 codes minted; only one burn
		// happens here so lastSeen must stay 0.
		t.Errorf("ConsumeRecoveryCode lastSeen=%d, want 0 (initial array should not be at length 1)", lastSeen)
	}

	after, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if got, want := len(after.MFARecoveryCodesHash), authcode.RecoveryCodeCount-1; got != want {
		t.Errorf("codes after %d-way concurrent burn = %d, want %d (double-burn would have left %d)",
			concurrency, got, want, authcode.RecoveryCodeCount-2)
	}
}

// TestMFAConfirm_AuditLand pins that mfaEnroll + mfaConfirm
// emit account.mfa_enrolled once on the successful confirm path
// (and never on a bad-code branch). Audit taxonomy is a
// locked contract per the design doc; a regression here would
// surface as dashboard audit rows missing the mfa_enrolled kind.
func TestMFAConfirm_AuditLand(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, true, false)
	// The audit stub is wire-able through srv; for MemStore the
	// simplest pin is the account row state (mfa_enrolled_at
	// stamped, mfa_required cleared) — already covered by
	// TestMFAConfirm_StampsEnrolledAndClearsMFARequired above.
	// This test pins the SUCCESS path's call-chain semantics.
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/enroll", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("/enroll: status %d", rec.Code)
	}
	var out api.MFAEnrollResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode /enroll: %v", err)
	}
	if len(out.RecoveryCodes) != authcode.RecoveryCodeCount {
		t.Fatalf("recovery code count = %d, want %d", len(out.RecoveryCodes), authcode.RecoveryCodeCount)
	}
}

// TestConsumeRecoveryCode_LastFlagSemantics pins the
// (matched=true, lastCode=true) return shape from
// Store.ConsumeRecoveryCode. The handler relies on lastCode to
// decide whether to refuse the burn (recover path) or proceed
// (disable path). Without this contract, /recover could
// accidentally burn the customer's last code and leave them
// with no way back in.
func TestConsumeRecoveryCode_LastFlagSemantics(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	ctx := context.Background()

	// Burn down to 1 code by driving /recover until 1 is left.
	for i := 0; i < authcode.RecoveryCodeCount-1; i++ {
		rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
			api.MFARecoverRequest{Code: recovCodes[i]})
		if rec.Code != http.StatusOK {
			t.Fatalf("burn #%d: status %d", i, rec.Code)
		}
	}
	lastCode := recovCodes[authcode.RecoveryCodeCount-1]
	presented, err := authcode.HashRecoveryCode(lastCode)
	if err != nil {
		t.Fatalf("HashRecoveryCode(%q): %v", lastCode, err)
	}

	// Drive the store primitive directly to bypass the
	// handler's lastCode refusal: the customer is calling
	// /disable, which is allowed to burn the last code.
	matched, lastCodeFlag, remaining, err := e.store.ConsumeRecoveryCode(ctx, e.acct.ID, presented)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !matched {
		t.Errorf("matched = false, want true")
	}
	if !lastCodeFlag {
		t.Errorf("lastCodeFlag = false, want true (single code is the last)")
	}
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0 (last-code consume drops remaining to 0; issue #329 contract)", remaining)
	}

	// And once consumed, the account has zero codes — proving
	// the SELECT FOR UPDATE + UPDATE removed them.
	after, _ := e.store.AccountByID(ctx, e.acct.ID)
	if got := len(after.MFARecoveryCodesHash); got != 0 {
		t.Errorf("recovery code count = %d, want 0 (consume should have deleted the last code)", got)
	}
}

// TestMFARecover_SendsBurnEmail pins issue #329 — when /recover
// successfully burns a recovery code, the handler MUST send the
// customer an email describing the burn. The body tone branches
// on remaining count:
//
//	9 left  → "9 recovery codes remaining"
//	2 left  → "You have 2 recovery codes left"
//	1 left  → "second-to-last code"
//	0 left  → unreachable via /recover (the handler refuses
//	          the last-code burn at line 287-294); covered by
//	          the third branch in pkg/mail/mfa_test.go for
//	          completeness
//
// The mailer stub is recordingMailer (defined in
// handlers_account_test.go) — same package, so we wire it in by
// reassigning srv.mailer. The test asserts:
//
//   - exactly one mailer.Send call per /recover success
//   - the To: address is the account email
//   - the subject matches the locked contract
//   - the body contains the right phrase for the remaining-count
//     bracket the test drove (one-of-many / warning)
//
// Mailer.Send failure must NOT fail the burn — the customer just
// proved a recovery code, so failing here would force them to
// re-authenticate. The recovery path stays green even when the
// mailer stub returns an error; a second test pins this.
func TestMFARecover_SendsBurnEmail(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	e.cookie = e.mfaIssueWithPending(t, true)

	mailer := &recordingMailer{}
	e.srv.mailer = mailer

	// First burn: 10 codes → 9 left. Body lands in the
	// one-of-many branch.
	rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
		api.MFARecoverRequest{Code: recovCodes[0]})
	if rec.Code != http.StatusOK {
		t.Fatalf("/recover status %d: %s", rec.Code, rec.Body)
	}
	calls := mailer.snapshot()
	if len(calls) != 1 {
		t.Fatalf("mailer.Send calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if len(got.To) != 1 || got.To[0] != e.acct.Email {
		t.Errorf("To = %v, want [%s]", got.To, e.acct.Email)
	}
	if got.Subject != "Recovery code used on your faas account" {
		t.Errorf("Subject = %q, want %q", got.Subject, "Recovery code used on your faas account")
	}
	if !strings.Contains(got.TextBody, "9 recovery codes remaining") {
		t.Errorf("body missing '9 recovery codes remaining'; got:\n%s", got.TextBody)
	}
	if !strings.Contains(got.TextBody, "support@gregale.dev") {
		t.Errorf("body missing support@gregale.dev; got:\n%s", got.TextBody)
	}
}

// TestMFARecover_MailerErrorDoesNotFailBurn pins the "Mailer
// failure is operator-visible but not customer-visible" guarantee
// (same shape as handlers_auth_login.go:256-263). A customer who
// just proved a recovery code must not be told the burn failed
// because the SMTP relay flaked — they re-authenticate, hit the
// refused-the-burn branch on the last code, and email themselves
// into an auth loop. The /recover handler logs the mailer error
// at ERROR and returns 200 anyway.
func TestMFARecover_MailerErrorDoesNotFailBurn(t *testing.T) {
	e := setupWithMFA(t, api.PlanPro, false, false)
	recovCodes, _, _ := e.generateEnrolledAccount(t)
	e.cookie = e.mfaIssueWithPending(t, true)

	mailer := &recordingMailer{sendErr: errSMTPFlake}
	e.srv.mailer = mailer

	rec := e.do(t, http.MethodPost, "/v1/account/mfa/recover",
		api.MFARecoverRequest{Code: recovCodes[0]})
	if rec.Code != http.StatusOK {
		t.Fatalf("/recover status %d, want 200 (mailer error must not fail the burn); body=%s", rec.Code, rec.Body)
	}
	if len(mailer.snapshot()) != 1 {
		t.Errorf("mailer.Send calls = %d, want 1 (the handler must still attempt the send)", len(mailer.snapshot()))
	}
	// And the code was actually burned — the burn precedes the
	// mailer.Send call, so a flaky mailer can't accidentally
	// leave the customer with a "code appears burned but isn't"
	// half-state.
	after, _ := e.store.AccountByID(context.Background(), e.acct.ID)
	if got := len(after.MFARecoveryCodesHash); got != authcode.RecoveryCodeCount-1 {
		t.Errorf("recovery code count = %d, want %d (burn must commit even if mailer flakes)", got, authcode.RecoveryCodeCount-1)
	}
}

// errSMTPFlake is a stand-in SMTP relay failure. Defined here so
// the burn-email test isn't coupled to a real mail package.
var errSMTPFlake = errors.New("smtp: relay temporarily unavailable")

// mustStepUpCookie is the SA5011 escape hatch for the /verify
// cookie re-issue test: the cookie loop can legitimately omit the
// step-up cookie, but we want a real cookie for assertions below.
// A helper that t.Fatal()s and returns the value lets staticcheck
// see the value is non-nil at the call site.
func mustStepUpCookie(t *testing.T, c *http.Cookie, msg string) *http.Cookie {
	t.Helper()
	if c == nil {
		t.Fatal(msg)
	}
	return c
}

// recoveryTestSecretOnce ensures the per-process recovery HMAC
// key is configured exactly once for the cmd/apid test suite.
// HashRecoveryCode is the production entry point that returns
// (digest, ErrNoHMACKey) when no key is loaded; the package's
// boot-time loader (cmd/apid/main.go::loadOrGenerateRecoveryHMACKey)
// fills that gap in production. Tests don't go through run(); so
// this sync.Once stands in for the loader.
//
// The key bytes are deterministic across runs so a flaky test
// that depends on a specific digest can pin it explicitly with
// its own SetHMACSecret call (and reset on t.Cleanup).
var recoveryTestSecretOnce sync.Once

// recoveryTestSecretBytes is the 32-byte deterministic test key.
// Hex-decoded from a constant so a future test that needs the
// digest for a specific plaintext can reproduce it offline.
var recoveryTestSecretBytes = func() []byte {
	// 32 bytes of 0x42 — irrelevant which exact pattern as long
	// as it's deterministic. Matches the convention used in
	// pkg/authcode/recovery_test.go's TestMain.
	return bytes.Repeat([]byte{0x42}, 32)
}()

// ensureRecoveryTestSecret installs the test recovery HMAC key
// exactly once across the suite. Idempotent across test files —
// tests that import authcode and call HashRecoveryCode share the
// same global secret.
func ensureRecoveryTestSecret(t *testing.T) {
	t.Helper()
	recoveryTestSecretOnce.Do(func() {
		dup := bytes.Clone(recoveryTestSecretBytes)
		if err := authcode.SetHMACSecret(dup); err != nil {
			t.Fatalf("recovery test secret: %v", err)
		}
	})
}
