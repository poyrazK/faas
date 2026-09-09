// MFA handlers (IAM-2, issue #186).
//
// Seven POST endpoints under /v1/account/mfa/*. MFA is opt-in: any
// authenticated dashboard user may start enrollment, and a
// confirmed authenticator is required for subsequent dashboard
// sessions. The wire shapes
// live in pkg/api/mfa.go; the audit taxonomy is locked:
//
//   - account.mfa_enrolled            — mfaConfirm success
//   - account.mfa_confirmed           — mfaConfirm success (alias)
//   - account.mfa_session_stepped_up  — mfaVerify success
//   - account.mfa_recovered           — mfaRecover success
//   - account.mfa_disabled            — mfaDisable success
//
// The failure-path rows (mfa_confirm_failed etc.) are emitted
// best-effort — useful for brute-force triage but not part of
// the locked taxonomy. The /v1/account/mfa/* routes are
// registered on authLimited+requireMFA+requireScope(adminOnly)
// in server.go; the requireMFA allowlist in mfa_middleware.go
// keeps them reachable while the session is mfa_pending.

package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/bindinghash"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// mfaRecipient is the host X25519 recipient apid loads once at
// boot for sealing TOTP secrets. Set by cmd/apid/main.go (or a
// test harness) via SetMFARecipient during boot — same shape
// as setSecretRecipient in handlers_secrets.go. nil means
// MFA enrollment is refused; the handler 503s CodeCapacity
// because there's no honest way to seal a TOTP secret without
// a host key.
var mfaRecipient func() *age.X25519Recipient

// errSessionMissingFromContext is returned by reissueSessionCookie
// (IAM-3 / ADR-039) when sessionFrom(r) yields no session — i.e.
// the handler reached a route that didn't run
// requireSessionCookie. Callers map this to 500 + log so the
// wiring bug surfaces instead of minting a sid-less envelope
// that the next request would 401 out of (a UX cliff).
var errSessionMissingFromContext = errors.New("reissueSessionCookie: no session in request context — wiring broken")

const mfaDisableEmailCooldown = 24 * time.Hour

// SetMFARecipient is the boot-time wire-up called by the apid
// main() once the host age key has loaded. Tests call this
// directly to inject an in-memory recipient.
func SetMFARecipient(f func() *age.X25519Recipient) { mfaRecipient = f }

// consumeRecoveryCodeSelector abstracts the atomic consume
// primitive Store.ConsumeRecoveryCode. PgStore does this with
// SELECT FOR UPDATE + UPDATE inside a pgx transaction; MemStore
// serialises via the existing mutex.
//
//   - `lastCode` is true iff the consumed code was the last
//     remaining one — the handler refuses to burn it and prompts
//     for the password instead, so the customer still has a way in.
//   - `remaining` is the count of hashes still on the row AFTER the
//     consume committed. The handler uses this to render the
//     post-burn customer email with the right tone (one-of-many
//     vs warning vs last-code) — see issue #329.
//
// Tests inject a stub to drive the success / miss / lastCode paths
// without touching a real DB.
type consumeRecoveryCodeSelector interface {
	ConsumeRecoveryCode(ctx context.Context, id string, presented []byte) (matched bool, lastCode bool, remaining int, err error)
}

// matchRecoveryCodeSelector abstracts the Store.MatchRecoveryCode
// read-only primitive. Same reasoning as the consume selector
// above — the handlers depend on the interface, the tests
// substitute a stub. PgStore + MemStore both implement it.
type matchRecoveryCodeSelector interface {
	MatchRecoveryCode(ctx context.Context, id string, presented []byte) (matched bool, lastCode bool, err error)
}

// matchRecoveryCode is a thin wrapper over Store.MatchRecoveryCode
// so the handler signature stays the same shape as
// consumeRecoveryCode. Tests inject the stub via the local
// interface.
func matchRecoveryCode(ctx context.Context, st matchRecoveryCodeSelector, accountID string, presented []byte) (matched bool, lastCode bool, err error) {
	return st.MatchRecoveryCode(ctx, accountID, presented)
}

// --- handlers ---------------------------------------------------------------

// mfaEnroll starts an MFA enrollment. Generates a fresh
// base32 TOTP secret + 10 recovery codes, seals the secret,
// persists the secret + SHA-256 hashes WITHOUT stamping
// mfa_enrolled_at, and returns the otpauth URL + secret + QR
// + recovery codes to the dashboard ONCE. The customer must
// then complete /confirm to commit the enrollment.
//
// 409 CodeConflict when the customer has already enrolled —
// the dashboard re-renders the "manage MFA" page instead.
func (s *server) mfaEnroll(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if acct.MFAEnrolled() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Already enrolled", "call /v1/account/mfa/disable before re-enrolling"))
		return
	}
	rec := mfaRecipient()
	if rec == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"MFA unavailable", "host age key not loaded — refusing to seal TOTP secret"))
		return
	}

	secret, err := auth.GenerateSecret()
	if err != nil {
		s.log.Error("mfa.enroll.generate_secret", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not generate TOTP secret"))
		return
	}
	key, err := auth.KeyFromSecret(secret, acct.Email)
	if err != nil {
		s.log.Error("mfa.enroll.key", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not derive TOTP key"))
		return
	}
	png, err := auth.QRCode(key)
	if err != nil {
		s.log.Error("mfa.enroll.qr", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not render TOTP QR"))
		return
	}
	plaintexts, hashes, err := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err != nil {
		s.log.Error("mfa.enroll.recovery", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not generate recovery codes"))
		return
	}

	sealed, err := secretbox.Seal(rec, secretbox.Envelope{"MFA_SECRET": secret})
	if err != nil {
		s.log.Error("mfa.enroll.seal", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not seal TOTP secret"))
		return
	}
	if err := s.store.SetMFASecret(r.Context(), acct.ID, sealed, hashes); err != nil {
		s.log.Error("mfa.enroll.persist", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not persist TOTP enrollment"))
		return
	}

	writeJSON(w, http.StatusOK, api.MFAEnrollResponse{
		OTPAuthURL:    key.URL(),
		Secret:        secret,
		QRCodePNG:     png,
		RecoveryCodes: plaintexts,
	})
}

// mfaConfirm finishes an MFA enrollment. Reads the sealed
// secret from the account row, unseals, verifies the presented
// 6-digit code against the current 30-s step (±1 skew).
// On success stamps mfa_enrolled_at + clears mfa_required, and
// re-issues the session cookie without mfa_pending.
//
// Not wrapped in s.idempotent: the customer should see the
// success body exactly once (the side-effects on the cookie +
// the store stamp are the load-bearing bits, and the dashboard
// reads the response status alone).
//
// CSRF (issue #186 review finding #7): the dashboard sends the
// CSRF token in the JSON body's `csrf_token` sibling. The
// verify call must run BEFORE decodeJSON so the body's
// io.ReadAll-then-restore invariant in extractRequestToken
// (pkg/middleware/csrf.go) holds — decodeJSON on a body that
// Verify already peeked would observe an empty stream.
func (s *server) mfaConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, "mfa_confirm", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	var req api.MFAConfirmRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "malformed JSON body"))
		return
	}
	if !auth.VerifyCode(readSealedSecret(s, w, r, acct.ID), req.Totp) {
		s.audit.Emit(r.Context(), "account.mfa_confirm_failed", &acct.ID, map[string]any{"reason": "code_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid code", "the TOTP code did not match"))
		return
	}
	if err := s.store.MarkMFAEnrolled(r.Context(), acct.ID); err != nil {
		s.log.Error("mfa.confirm.mark_enrolled", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not finalize enrollment"))
		return
	}
	if err := s.reissueSessionCookie(w, r, acct, false); err != nil {
		// Cookie re-issue failure is non-fatal — the customer's
		// enrollment landed; the next request will trip
		// requireMFA until they log in again. We log it but
		// still return 200 because the wire contract for
		// /confirm is "enrollment landed".
		//
		// Exception: the wiring-bug sentinel (no session in
		// context) is a 5xx-level bug, not a sealing failure —
		// log at Error so it can't quietly hide.
		if errors.Is(err, errSessionMissingFromContext) {
			s.log.Error("mfa.confirm.reissue_cookie.wiring", "err", err.Error())
		} else {
			s.log.Warn("mfa.confirm.reissue_cookie", "err", err.Error())
		}
	}
	s.audit.Emit(r.Context(), "account.mfa_enrolled", &acct.ID, nil)
	s.audit.Emit(r.Context(), "account.mfa_confirmed", &acct.ID, map[string]any{"method": "totp"})
	writeJSON(w, http.StatusOK, api.MFAConfirmResponse{})
}

// mfaVerify steps up an mfa_pending session. Same verify path
// as /confirm but does NOT stamp mfa_enrolled_at (the customer
// is already enrolled). On success re-issues the session cookie
// without mfa_pending — the next protected route request
// passes through the requireMFA gate cleanly.
//
// 401 CodeMFAInvalidCode on a bad code, 404 if the account row
// is missing (defense in depth — should never happen because
// s.auth resolved it moments earlier).
func (s *server) mfaVerify(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.MFAVerifyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "malformed JSON body"))
		return
	}
	if !auth.VerifyCode(readSealedSecret(s, w, r, acct.ID), req.Totp) {
		s.audit.Emit(r.Context(), "account.mfa_verify_failed", &acct.ID, map[string]any{"reason": "code_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid code", "the TOTP code did not match"))
		return
	}
	if err := s.reissueSessionCookieWithStepUp(w, r, acct, false, time.Now()); err != nil {
		s.log.Error("mfa.verify.reissue_cookie", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not re-issue session cookie"))
		return
	}
	s.audit.Emit(r.Context(), "account.mfa_session_stepped_up", &acct.ID, nil)
	s.audit.Emit(r.Context(), "auth.step_up_verified", &acct.ID, map[string]any{
		"path":    r.URL.Path,
		"method":  r.Method,
		"ttl_sec": 300, // 5-minute freshness window per ADR-077
	})
	writeJSON(w, http.StatusOK, api.MFAVerifyResponse{})
}

// mfaRecover burns a recovery code to regain access when the
// TOTP device is lost. Removes the matching SHA-256 hash from
// the stored set; re-issues the session cookie without
// mfa_pending. The customer can still hit /verify if they
// re-install the authenticator app and recover the device.
//
// consumeRecoveryCode does the SELECT FOR UPDATE + UPDATE
// dance so two concurrent /recover requests can't both think
// they burned the same code. (Defense in depth — the dashboard
// disables the form while one request is in flight.)
//
// CSRF (issue #186 review finding #7): a recovery consumes a
// stored code (a state-changing side effect) — same
// verify-before-decodeJSON contract as /confirm.
//
// Top-level handler stays under the 50-line rule (CLAUDE.md
// "Handlers ≤ 50 lines") by extracting the match → refuse-last
// sequence into refuseLastRecoveryCodeOrMatch, and the
// consume → reissue-cookie → audit → email sequence into
// completeRecoveryCodeBurn. The two helpers together own the
// "two-phase" pattern: match then consume, with the customer's
// last-code refuse path atomic on the store side (Finding #5).
func (s *server) mfaRecover(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, "mfa_recover", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	var req api.MFARecoverRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "malformed JSON body"))
		return
	}
	// Hash the presented code with the per-process HMAC key.
	// The HMAC key is loaded at apid boot via cmd/apid/main.go
	// (FAAS_MFA_RECOVERY_HMAC_KEY / /var/lib/faas/recovery-hmac.key).
	// If it is missing the boot refuses to start, so this path
	// can only fail on (a) a programming error — secret unset
	// at test time without SetHMACSecret — or (b) a runtime
	// reset, neither of which is reachable in production.
	presented, err := authcode.HashRecoveryCode(req.Code)
	if err != nil {
		s.log.Error("mfa.recover.hash", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not hash recovery code"))
		return
	}
	// Step 1 — match without mutating. The split (match → refuse
	// → consume) lets us reject the last-code burn atomically,
	// rather than burning first and noticing later (issue #186
	// review Finding #5). The refuse/wrong-code paths write
	// their own response + audit row inside the helper.
	ok := s.refuseLastRecoveryCodeOrMatch(w, r, acct, presented)
	if !ok {
		return
	}
	// Step 2 — consume, reissue cookie, audit, email, write 200.
	s.completeRecoveryCodeBurn(w, r, acct, presented)
}

// refuseLastRecoveryCodeOrMatch is Step 1 of mfaRecover. It runs
// the non-mutating MatchRecoveryCode primitive, refuses the burn
// on the customer's last code (or on a no-match), and returns
// true iff the burn is safe to proceed with. Writes its own
// response on every refusal path; the caller only sees `ok`.
//
// Returns `ok == true` only when MatchRecoveryCode returned
// (matched=true, lastCode=false). On that path the store is
// unchanged — we haven't consumed anything yet — so a
// concurrent caller seeing the same code would still see the
// code present (defence in depth with the consume-side
// SELECT FOR UPDATE).
func (s *server) refuseLastRecoveryCodeOrMatch(w http.ResponseWriter, r *http.Request, acct state.Account, presented []byte) bool {
	matched, lastCode, err := matchRecoveryCode(r.Context(), s.store, acct.ID, presented)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
				"Invalid code", "no matching recovery code"))
			return false
		}
		s.log.Error("mfa.recover.match", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not match recovery code"))
		return false
	}
	if !matched {
		s.audit.Emit(r.Context(), "account.mfa_recover_failed", &acct.ID, map[string]any{"reason": "code_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid code", "no matching recovery code"))
		return false
	}
	if lastCode {
		// Refuse to burn the only remaining code: the customer
		// would otherwise be locked out with no way back in.
		// The store still has the code (matched-then-not-burnt).
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Last recovery code",
			"burning the only remaining code would lock you out — use /v1/account/mfa/disable with your password instead"))
		return false
	}
	return true
}

// completeRecoveryCodeBurn is Step 2 of mfaRecover. Consumes the
// matched code (SELECT FOR UPDATE + UPDATE under a tx), reissues
// the session cookie without mfa_pending, emits the
// `account.mfa_recovered` audit row, fires the post-burn email
// (issue #329), and writes the 200 OK. Any failure on the way
// writes an error response; callers don't need to.
//
// Concurrency note (race fix): the consume is the atomic
// match-and-remove primitive; two concurrent /recover calls
// serialise under the store's row lock (MemStore: sync.Mutex;
// PgStore: SELECT FOR UPDATE). Both calls' Step-1
// MatchRecoveryCode can return matched=true before either
// reaches this Step-2 consume. The first caller wins the
// match-and-remove; the second caller's consume returns
// matched=false. Without checking matched here, the second
// caller would also emit 200 — that is the (200=2, 401=0)
// flake TestMFARecover_ConcurrentBurnOneCode trips on a fast
// machine. Checking matched surfaces the lost race as a 401
// ("code already consumed") with a code_raced audit row. The
// customer's last-code UX (the 409 path inside
// refuseLastRecoveryCodeOrMatch) is preserved because that
// path never reaches consume — MatchRecoveryCode is
// non-mutating per the issue #186 Finding #5 fix.
//
// Email send failure is intentionally NOT a hard failure: the
// burn has already committed and the customer's session is
// already reissued, so failing the response would force the
// customer to re-authenticate. Same shape as the password-reset
// mailer at handlers_auth_login.go:256-263.
func (s *server) completeRecoveryCodeBurn(w http.ResponseWriter, r *http.Request, acct state.Account, presented []byte) {
	matchedConsume, _, remaining, err := consumeRecoveryCode(r.Context(), s.store, acct.ID, presented)
	if err != nil {
		s.log.Error("mfa.recover.consume", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not consume recovery code"))
		return
	}
	if !matchedConsume {
		// Another /recover call (or a /disable path) burned the
		// code between our MatchRecoveryCode and our consume.
		// The code is no longer valid for this account.
		s.audit.Emit(r.Context(), "account.mfa_recover_failed", &acct.ID, map[string]any{"reason": "code_raced"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid code", "recovery code was already consumed"))
		return
	}
	if err := s.reissueSessionCookie(w, r, acct, false); err != nil {
		s.log.Error("mfa.recover.reissue_cookie", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not re-issue session cookie"))
		return
	}
	s.audit.Emit(r.Context(), "account.mfa_recovered", &acct.ID, nil)
	s.sendBurnEmail(r.Context(), acct, remaining)
	writeJSON(w, http.StatusOK, api.MFARecoverResponse{})
}

// sendBurnEmail fires the post-burn customer email described in
// issue #329. `remaining` is the count of hashes left on the row
// AFTER the consume committed — pkg/mail.RecoveryCodeBurnedBody
// uses it to pick the right tone bucket (one-of-many / warning /
// last-code). Mailer failure is logged at ERROR and swallowed;
// the burn has already committed and the customer's session
// cookie has been re-issued, so failing the response here would
// force them to re-authenticate. Mirrors the password-reset
// mailer pattern at handlers_auth_login.go:256-263.
func (s *server) sendBurnEmail(ctx context.Context, acct state.Account, remaining int) {
	subject, body := mailpkg.RecoveryCodeBurnedBody(acct.Email, remaining, time.Now().UTC())
	if err := s.mailer.Send(ctx, Message{
		To:       []string{acct.Email},
		Subject:  subject,
		TextBody: body,
	}); err != nil {
		s.log.Error("mfa.recover.mailer", "err", err.Error())
	}
}

// mfaDisable opts the customer out of MFA. Body {password} OR
// {recovery_code} — the customer must prove they hold the
// second factor (or the password fallback) to disable. This
// is the analog of /v1/account/delete's password re-prompt.
//
// Clears mfa_secret_encrypted + mfa_recovery_codes_hash +
// mfa_enrolled_at. Leaves mfa_required untouched so an explicit
// policy remains in force after disable.
//
// CSRF (issue #186 review finding #7): ClearMFA is irreversible
// from the customer's perspective (re-enroll is the only path
// back), so the CSRF gate sits between the cookie issuance and
// any state change.
//
// Helper extraction (issue #186 review finding polish):
// the password-verify and recovery-consume branches are
// extracted to disableByPassword / disableByRecoveryCode so the
// top-level handler stays under the 50-line rule (CLAUDE.md
// "Handlers ≤ 50 lines").
func (s *server) mfaDisable(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, "mfa_disable", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	var req api.MFADisableRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "malformed JSON body"))
		return
	}
	hasPassword := req.Password != ""
	hasRecovery := req.RecoveryCode != ""
	if hasPassword == hasRecovery { // both or neither
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "exactly one of password or recovery_code is required"))
		return
	}

	switch {
	case hasPassword:
		if !s.disableByPassword(w, r, acct, req.Password) {
			return
		}
	default:
		if !s.disableByRecoveryCode(w, r, acct, req.RecoveryCode) {
			return
		}
	}

	if err := s.store.ClearMFA(r.Context(), acct.ID); err != nil {
		s.log.Error("mfa.disable.clear", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not clear MFA state"))
		return
	}
	method := "password"
	if !hasPassword {
		method = "recovery_code"
	}
	s.audit.Emit(r.Context(), "account.mfa_disabled", &acct.ID, map[string]any{"method": method})
	writeJSON(w, http.StatusOK, api.MFADisableResponse{})
}

// mfaDisableEmail starts the email-assisted MFA disable path. The token is
// cryptographically random and only its hash is stored; the email link is
// useless until the server-side 24-hour cooldown has elapsed.
func (s *server) mfaDisableEmail(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, "mfa_disable_email", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	var req api.MFADisableEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "malformed JSON body"))
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("mfa.disable_email.rand", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not create MFA disable request"))
		return
	}
	if mfaRecipient == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"MFA unavailable", "host age key not loaded — refusing to issue disable token"))
		return
	}
	rec := mfaRecipient()
	if rec == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"MFA unavailable", "host age key not loaded — refusing to issue disable token"))
		return
	}
	sealed, err := secretbox.SealBytes(rec, "mfa-disable", raw, 64)
	if err != nil {
		s.log.Error("mfa.disable_email.seal", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not create MFA disable request"))
		return
	}
	requestedAt := time.Now().UTC()
	if err := s.store.IssueMFADisableRequest(r.Context(), api.HashToken(sealed), acct.ID, requestedAt); err != nil {
		s.log.Error("mfa.disable_email.issue_request", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not create MFA disable request"))
		return
	}
	link := s.mfaDisableEmailLink(r, sealed)
	subject, body := mailpkg.MFADisableEmailRequestedBody(acct.Email, link, requestedAt)
	if err := s.mailer.Send(r.Context(), Message{To: []string{acct.Email}, Subject: subject, TextBody: body}); err != nil {
		s.log.Error("mfa.disable_email.mailer", "err", err.Error())
	}
	s.audit.Emit(r.Context(), "account.mfa_disable_email_requested", &acct.ID, nil)
	writeJSON(w, http.StatusOK, api.MFADisableEmailResponse{})
}

// mfaDisableEmailConfirm consumes a live emailed token after the server-side
// cooldown and clears the account's MFA state. The account binding is checked
// before consume so a token cannot be used from another authenticated account.
func (s *server) mfaDisableEmailConfirm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, "mfa_disable_email_confirm", acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	var req api.MFADisableEmailConfirmRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid request", "malformed JSON body"))
		return
	}
	sealed, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(req.Token))
	if err != nil || len(sealed) == 0 {
		s.writeMFADisableEmailInvalid(w, r, acct, "token_malformed")
		return
	}
	idents := s.mfaDisableTokenIdentities()
	if len(idents) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"MFA unavailable", "host age identity not loaded — refusing to verify disable token"))
		return
	}
	namespace, plaintext, err := secretbox.OpenBytesMulti(idents, sealed)
	if err != nil || namespace != "mfa-disable" || len(plaintext) != 32 {
		s.writeMFADisableEmailInvalid(w, r, acct, "token_invalid")
		return
	}
	reqRow, err := s.store.GetMFADisableRequest(r.Context(), api.HashToken(sealed))
	if err != nil || reqRow.AccountID != acct.ID {
		s.writeMFADisableEmailInvalid(w, r, acct, "token_invalid")
		return
	}
	readyAt := reqRow.RequestedAt.Add(mfaDisableEmailCooldown)
	if now := time.Now().UTC(); now.Before(readyAt) {
		retryAfter := int(time.Until(readyAt).Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		api.WriteProblem(w, api.NewProblem(http.StatusTooEarly, api.CodeMFADisableCooldown,
			"MFA disable cooldown active", "confirm this request after the 24-hour waiting period").WithHeader("Retry-After", strconv.Itoa(retryAfter)))
		return
	}
	if _, err := s.store.ConsumeMFADisableRequest(r.Context(), api.HashToken(sealed)); err != nil {
		s.writeMFADisableEmailInvalid(w, r, acct, "token_replayed")
		return
	}
	if err := s.store.ClearMFA(r.Context(), acct.ID); err != nil {
		s.log.Error("mfa.disable_email.clear", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not clear MFA state"))
		return
	}
	s.audit.Emit(r.Context(), "account.mfa_disabled", &acct.ID, map[string]any{"method": "email"})
	writeJSON(w, http.StatusOK, api.MFADisableEmailConfirmResponse{})
}

func (s *server) writeMFADisableEmailInvalid(w http.ResponseWriter, r *http.Request, acct state.Account, reason string) {
	s.audit.Emit(r.Context(), "account.mfa_disable_email_failed", &acct.ID, map[string]any{"reason": reason})
	api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
		"Invalid token", "the MFA disable link is invalid or has already been used"))
}

func (s *server) mfaDisableEmailLink(r *http.Request, raw []byte) string {
	scheme := schemeHTTP
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
		scheme = schemeHTTPS
	}
	host := r.Host
	if s.domain != "" && s.domain != domainUnset {
		host = s.domain
		scheme = schemeHTTPS
	}
	return fmt.Sprintf("%s://%s/v1/account/mfa/disable-email/confirm?token=%s", scheme, host, base64.RawURLEncoding.EncodeToString(raw))
}

func (s *server) mfaDisableTokenIdentities() []*age.X25519Identity {
	if mfaIdentities != nil {
		if idents := mfaIdentities(); len(idents) > 0 {
			return idents
		}
	}
	if mfaIdentity != nil {
		if id := mfaIdentity(); id != nil {
			return []*age.X25519Identity{id}
		}
	}
	return nil
}

// disableByPassword re-authenticates the customer via their
// stored password hash (PHC compare, same helper /login uses).
// Returns true on a successful verify; false after writing a
// 401 problem to w on a failure path. The audit Emit fires
// before each 401 so brute-force attempts leave a trail in the
// events table.
func (s *server) disableByPassword(w http.ResponseWriter, r *http.Request, acct state.Account, presented string) bool {
	hash, err := s.store.AccountPasswordByAccountID(r.Context(), acct.ID)
	if err != nil || hash == "" {
		s.audit.Emit(r.Context(), "account.mfa_disable_failed", &acct.ID, map[string]any{"reason": "no_password"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid credentials", "password not set on this account"))
		return false
	}
	ok, err := auth.Verify(hash, presented)
	if err != nil || !ok {
		s.audit.Emit(r.Context(), "account.mfa_disable_failed", &acct.ID, map[string]any{"reason": "password_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid credentials", "password did not match"))
		return false
	}
	return true
}

// disableByRecoveryCode consumes one of the customer's stored
// recovery codes. Identical helper to /recover; the only
// difference is the surrounding audit taxonomy (disable vs
// recover). We deliberately burn the LAST code on this path —
// the customer is about to ClearMFA anyway, so the locked-out
// terminal state from /recover doesn't apply here.
func (s *server) disableByRecoveryCode(w http.ResponseWriter, r *http.Request, acct state.Account, presented string) bool {
	presentedHash, err := authcode.HashRecoveryCode(presented)
	if err != nil {
		// HMAC secret unset — boot-time apid refuses to start
		// without one (cmd/apid/main.go::LoadRecoveryHMACKey);
		// a runtime unset is a programming error. Surface as a
		// capacity-style 5xx so the dashboard renders a retry
		// prompt rather than silently letting the customer
		// through with an unmatched code.
		s.log.Error("mfa.disable.hash", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not hash recovery code"))
		return false
	}
	// `remaining` is discarded on this path: the customer is about
	// to ClearMFA anyway, so the count of codes left on the row is
	// irrelevant. The /recover handler is the one that hands the
	// remaining count to the mailer (see mfaRecover above) — issue
	// #329's burn-email path doesn't apply to /disable.
	matched, _, _, err := consumeRecoveryCode(r.Context(), s.store, acct.ID, presentedHash)
	if err != nil || !matched {
		s.audit.Emit(r.Context(), "account.mfa_disable_failed", &acct.ID, map[string]any{"reason": "code_mismatch"})
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeMFAInvalidCode,
			"Invalid code", "no matching recovery code"))
		return false
	}
	return true
}

// --- helpers ----------------------------------------------------------------

// readSealedSecret returns the plaintext TOTP secret for an
// account, unsealing the at-rest blob with the host age
// identity. Writes a 5xx problem on any error and returns ""
// — the verify caller checks for the empty string and 500s
// upstream; the helper deliberately does NOT retry, because a
// failing seal means a real boot config error.
//
// (The helper shape mirrors readAppSecret in handlers_secrets.go
// — same idea, separate seal key/value.)
func readSealedSecret(s *server, w http.ResponseWriter, r *http.Request, accountID string) string {
	encrypted, err := s.store.ReadMFASecret(r.Context(), accountID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Not enrolled", "complete /v1/account/mfa/enroll first"))
			return ""
		}
		s.log.Error("mfa.read_sealed_secret", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not read MFA state"))
		return ""
	}
	// mfaRecipient is read at enroll time but here we need the
	// *identity* to unseal. The identity is the host's age
	// private key — guarded by the same boot-time wire-up as
	// the recipient. nil identity = the daemon started without
	// a host age key, which is the same CodeCapacity condition
	// as mfaEnroll refusing to seal.
	//
	// mfaIdentities returns the multi-identity slice loaded by
	// secretbox.LoadHostKeys(dir) — current first, previous
	// second during the 30-day rotation overlap window
	// (issue #316 / ADR-057). OpenMulti is the rotation-aware
	// entry point. Fall back to the single-identity accessor so
	// pre-rotation test harnesses (and any caller that hasn't
	// migrated to LoadHostKeys) keep working.
	var idents []*age.X25519Identity
	if mfaIdentities != nil {
		idents = mfaIdentities()
	}
	if len(idents) == 0 && mfaIdentity != nil {
		if single := mfaIdentity(); single != nil {
			idents = []*age.X25519Identity{single}
		}
	}
	if len(idents) == 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"MFA unavailable", "host age identity not loaded — refusing to unseal"))
		return ""
	}
	env, err := secretbox.OpenMulti(idents, encrypted)
	if err != nil {
		s.log.Error("mfa.open_sealed_secret", "err", err.Error())
		api.WriteProblem(w, api.ErrCapacity("could not unseal TOTP secret"))
		return ""
	}
	return env["MFA_SECRET"]
}

// mfaIdentity is the boot-time-wired host age identity (private
// key). Same pattern as mfaRecipient, but for Open() instead of
// Seal(). nil means the daemon hasn't loaded the host key yet
// — the handler 503s CodeCapacity.
//
// Deprecated for new callers: use mfaIdentities + secretbox.OpenMulti
// instead (issue #316 / ADR-057 rotation overlap). Kept as a
// backward-compat seam so existing tests that inject a single
// identity continue to work.
var mfaIdentity func() *age.X25519Identity

// SetMFAIdentity is the boot-time wire-up called by apid
// main(). Tests call this directly to inject an in-memory
// identity.
func SetMFAIdentity(f func() *age.X25519Identity) { mfaIdentity = f }

// mfaIdentities is the rotation-aware identity accessor: it
// returns the slice loaded by secretbox.LoadHostKeys(dir) —
// current first, previous second during the 30-day overlap
// window. nil means "no host.age loaded yet"; the handler 503s
// CodeCapacity, matching the single-identity contract.
var mfaIdentities func() []*age.X25519Identity

// SetMFAIdentities wires the multi-identity accessor. Called
// from cmd/apid/main.go after secretbox.LoadHostKeys returns.
// Existing SetMFAIdentity callers (mostly tests) continue to
// work unchanged — the single-identity accessor is still wired.
func SetMFAIdentities(f func() []*age.X25519Identity) { mfaIdentities = f }

// reissueSessionCookie mints a fresh session cookie (with the
// given mfa-pending flag) and sets it on the response. Used by
// /confirm, /verify, /recover to clear the mfa_pending flag
// after a successful step-up. Mirrors issueSessionCookie but
// takes the mfaPending explicitly so the verify step-up can clear
// the current session's pending state even though the account's
// enrollment or explicit policy still requires MFA on future
// sessions.
//
// IAM-3 (ADR-039, issue #187 + #244 merged): this path REUSES
// the existing sid — it reads the current state.Session out of
// the request context (stamped by requireSessionCookie in the
// cookie branch of s.auth) and seals a new envelope with the
// SAME sid and the flipped mfaPending. No second sessions row
// is created; the row's last_seen_at is bumped by the debounce
// touch in requireSessionCookie just before this call lands.
//
// If sessionFrom(r) returns false the wiring is broken — the
// handler reached this code via a route that didn't run
// requireSessionCookie. We fail closed with a sentinel error
// rather than mint a sid-less envelope; the silent-fallback
// path was a UX cliff (next request would 401
// CodeSessionExpired and log the customer out of an action
// they just completed). Callers already map this error to
// 500 + a log line, so the wiring bug surfaces instead of
// hiding.
func (s *server) reissueSessionCookie(w http.ResponseWriter, r *http.Request, acct state.Account, mfaPending bool) error {
	return s.reissueSessionCookieWithStepUp(w, r, acct, mfaPending, time.Time{})
}

// reissueSessionCookieWithStepUp is the IAM-hardening-mega-PR
// (logical change 6, ADR-077) entry point. Pass a non-zero
// stepUpAt to refresh the step-up stamp on the cookie envelope
// (the /v1/account/mfa/verify success branch sets it to
// time.Now()); pass time.Time{} to leave it cleared (the
// enrollment-confirm + recovery + disable branches don't
// refresh step-up because they don't gate on it themselves).
//
// The binding-hash is unconditionally refreshed so the
// stolen-cookie auto-revoke branch (ADR-076) always sees the
// current (IP, UA-family) fingerprint. Two writes land in
// lockstep before the cookie is sealed:
//
//  1. store.UpdateSessionBinding(current.ID, acct.ID, bind)
//     re-stamps sessions.binding_hash so the row's fingerprint
//     matches the cookie envelope's. Without this the
//     middleware cross-check (RequireSessionCookie step 3.5,
//     pkg/auth/middleware/middleware.go) would compare the
//     new envelope hash against the row's stale mint-time
//     hash and trip the auto-revoke branch on the customer's
//     own post-reissue request.
//
//  2. sessions.IssueWithSessionAndBindingHash[AndStepUp]
//     seals the envelope with the SAME bind value and writes
//     it as Set-Cookie.
//
// If step 1 fails (ErrNotFound = row already revoked by an
// interleaving operator or by the auto-revoke branch itself)
// we surface the failure so the caller can map to 401
// CodeSessionInvalid; minting a new envelope over a revoked
// row would silently re-arm the customer's session.
func (s *server) reissueSessionCookieWithStepUp(w http.ResponseWriter, r *http.Request, acct state.Account, mfaPending bool, stepUpAt time.Time) error {
	current, ok := authmw.SessionFromContext(r)
	if !ok {
		if s.log != nil {
			s.log.Error("reissueSessionCookie: no session in context — refusing to mint sid-less envelope",
				"path", r.URL.Path, "account", acct.ID)
		}
		return errSessionMissingFromContext
	}
	bind := bindinghash.Compute(clientIPFromRequest(r), bindinghash.UAFamily(r.UserAgent()), s.bindingKeyFn)
	if err := s.store.UpdateSessionBinding(r.Context(), current.ID, acct.ID, bind); err != nil {
		if s.log != nil {
			s.log.Warn("reissueSessionCookie: update binding_hash failed",
				"path", r.URL.Path, "account", acct.ID, "err", err.Error())
		}
		return err
	}
	var cookie string
	var err error
	if stepUpAt.IsZero() {
		cookie, err = s.sessions.IssueWithSessionAndBindingHash(current.ID, acct.ID, bind, mfaPending)
	} else {
		cookie, err = s.sessions.IssueWithSessionAndBindingHashAndStepUp(current.ID, acct.ID, bind, stepUpAt, mfaPending)
	}
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.sessions.MaxAge().Seconds()),
	})
	return nil
}

// consumeRecoveryCode is a thin wrapper over Store.ConsumeRecoveryCode
// that exists so the handler tests can stub the consume path via the
// minimal `consumeRecoveryCodeSelector` interface without faking a
// full Store. PgStore + MemStore both implement
// `ConsumeRecoveryCode(ctx, id, presented) (matched, lastCode, err)`
// directly, so the wrapper just forwards.
//
// Returns:
//
//   - matched=true,  lastCode=true   — a code was burned; it was the
//     last remaining one (the caller should refuse the burn and
//     prompt the customer to use a password instead, except when
//     the caller's intent is to ClearMFA immediately afterward, in
//     which case the burn is welcome).
//   - matched=true,  lastCode=false  — a code was burned; more remain
//   - matched=false, lastCode=false  — no match (the customer typed
//     a wrong code, or replayed an already-burned one)
//   - err == state.ErrNotFound       — account row missing
//
// Single-row serialization is guaranteed by the Store: MemStore holds
// m.mu over the whole read+compare+write; PgStore wraps it in a tx
// with SELECT … FOR UPDATE on accounts. Two concurrent /recover
// requests on the same account cannot both observe and burn the same
// hash.
func consumeRecoveryCode(ctx context.Context, st consumeRecoveryCodeSelector, accountID string, presented []byte) (matched bool, lastCode bool, remaining int, err error) {
	return st.ConsumeRecoveryCode(ctx, accountID, presented)
}
