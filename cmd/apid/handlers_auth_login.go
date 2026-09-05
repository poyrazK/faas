// Email + password auth surface (issue #165 PR #2, ADR-032).
//
// PR #1 (commit 21267fd) closed the literal #165 takeover by replacing
// POST /login's auto-account-create + key-mint path with a one-shot
// fallback: an X-Dashboard-Key header carrying a pre-existing
// "web-console" API key. That fallback is intentionally not the
// long-term surface. PR #2 lands the real auth surface that replaces
// it: email + password (Argon2id), password reset via the existing
// login_tokens table, and a set-password escape hatch for OAuth-only
// customers.
//
// Anti-enumeration shape (spec §11): every authentication outcome
// returns 401 invalid_credentials with the same body, regardless of
// whether the email is unbound, the password is wrong, or the account
// has no password row. The constant-time Argon2id pad on the no-row
// path (pkg/auth.DummyPHC) closes the timing oracle: both branches
// pay one Argon2id verify under identical parameters.
//
// Forgot-password shape (spec §11): POST /login/forgot always returns
// 200 with an identical body regardless of whether the email exists.
// The reset URL is mailed via the platform's Mailer; the response
// never leaks whether the email is bound to an account.
//
// Set-password shape (post-OAuth opt-in): POST /dashboard/account/set-password
// lets OAuth-only customers opt into password login. Behind sessionAuth
// so the call is anchored to a known account.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// passwordLoginPaths is the canonical set of dashboard auth routes
// PR #2 wires. The constants are local to this file so the test
// surface (handlers_auth_login_test.go) reuses the same names.
const (
	signupPath         = "/signup"
	passwordForgotPath = "/login/forgot"
	resetFormPath      = "/auth/reset"
	resetSubmitPath    = "/auth/reset"
	resetTokenPath     = "/auth/reset"
	setPasswordPath    = "/dashboard/account/set-password"
	logoutPathPublic   = "/logout"

	// domainUnset is the sentinel value apid uses to mean
	// "no canonical domain configured" — distinct from "" (which
	// triggers the dev-mode defaults in other handlers). The
	// forgot-password path treats both the empty string and
	// domainUnset as "use the request Host verbatim" so a misconfigured
	// dev deploy never emails a customer a link stamped with a literal
	// "unset" string in place of their hostname.
	// schemeHTTP / schemeHTTPS live in handlers_google.go alongside
	// googleAuthStateCookie so all auth handlers share them.
	domainUnset = "unset"

	// passwordResetTTL is how long a reset token stays valid. 15 min
	// matches industry convention (NIST SP 800-63B password recovery
	// guidance) and is short enough that a leaked email doesn't
	// outlive the customer's session window.
	passwordResetTTL = 15 * time.Minute
	// emailVerificationTTL is deliberately longer than a login/reset link:
	// it proves address ownership and does not authenticate the browser.
	emailVerificationTTL = 24 * time.Hour
	// emailVerificationGrace is surfaced in the persistent dashboard notice.
	// Sensitive deploy and payment actions still require verification now.
	emailVerificationGrace = 30 * 24 * time.Hour
	emailVerificationPath  = "/v1/auth/verify-email"
)

// postLogin is the PR #2 (issue #165) password login path. JSON body
// or form-encoded body; the handler canonicalises the email (trim +
// lowercase) and Argon2id-verifies the password against the
// account_passwords row.
//
// Three terminal outcomes, all 401 invalid_credentials with the same
// body:
//   - email unbound: no AccountByEmail hit → run the Argon2id pad
//     against DummyPHC so the timing matches the "wrong-password"
//     path; return 401.
//   - account exists but no password row (OAuth-only): Argon2id pad
//   - 401.
//   - account exists, password row exists, verify fails: 401.
//
// Successful verify: mint a session cookie, write a JSON body with
// only {account_id, plan} — NO api_key field. The session cookie is
// the only auth artifact; programmatic auth stays on the device-code
// flow (cmd/apid/handlers_cli_auth.go).
func (s *server) postLoginEmail(w http.ResponseWriter, r *http.Request) {
	email, password, ok := decodeEmailPasswordRequest(w, r)
	if !ok {
		return
	}
	acct, ok := s.verifyPasswordOrPad(r.Context(), email, password)
	if !ok {
		// Issue #286: emit a failed-login audit row on the
		// async-batched channel. The discriminator is the same
		// at every emit site (ip + email_hash + user_agent) so
		// the audit reader does not need to disambiguate the
		// three failure modes collapsed into one 401 by
		// verifyPasswordOrPad. The 401 response is unaffected
		// by the audit row — EmitFailedLogin is non-blocking.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			auth.HashEmail(email),
			r.UserAgent())
		api.WriteProblem(w, api.ErrInvalidCredentials())
		return
	}
	if !acct.EmailVerified() {
		s.sendEmailVerification(r.Context(), r, acct)
	}
	s.issueSessionCookie(w, r, acct)
	writeLoginJSON(w, acct)
}

// postSignup is the customer-facing POST /signup. Three outcomes:
//   - email unbound: create the account, set the password, sign in.
//   - email bound but the supplied password matches the existing
//     password row: sign in (idempotent — the same email+password
//     pair may retry freely).
//   - email bound and the supplied password does NOT match the
//     existing password row: 401 invalid_credentials (NEVER 409 —
//     an attacker cannot use /signup to enumerate accounts).
//
// This is the anti-enumeration closure for the create-vs-claim race:
// pre-#165 customers who created their account via the buggy
// handler can "recover" by signing up with the same email + the
// password they want, and the existing password row is overwritten.
func (s *server) postSignup(w http.ResponseWriter, r *http.Request) {
	email, password, ok := decodeEmailPasswordRequest(w, r)
	if !ok {
		return
	}
	if err := auth.Validate(password); err != nil {
		api.WriteProblem(w, api.ErrPasswordTooWeak(err.Error()))
		return
	}

	acct, err := s.store.AccountByEmail(r.Context(), email)
	if err != nil {
		// Email is unbound: create the account, set the password,
		// sign in. CreateAccount on email uniqueness violation is
		// the race we close here: a concurrent signup for the same
		// email will return state.ErrConflict; we collapse to the
		// sign-in path so the duplicate caller signs in (idempotent)
		// rather than learning "this email is taken".
		res, createErr := s.store.CreateAccountWithPersonalOrg(r.Context(), state.CreateAccountWithPersonalOrgParams{
			Email:                    email,
			Plan:                     api.PlanFree,
			RequireEmailVerification: true,
		})
		created := res.Account
		if createErr != nil {
			if errors.Is(createErr, state.ErrConflict) {
				// Concurrent signup. Re-issue the verify path so
				// the response shape matches the post-create path.
				existing, ok := s.verifyPasswordOrPad(r.Context(), email, password)
				if !ok {
					// Newcomer would have set this password; the
					// existing account has a different one. We
					// MUST NOT reveal the difference (anti-enumeration).
					// Issue #286: emit failed-login on the same
					// async-batched channel — the emit site is
					// the same as the postLoginEmail path so the
					// audit row shape is identical and the
					// operator cannot distinguish a /login
					// wrong-password from a /signup wrong-password
					// (which is the desired posture).
					s.audit.EmitFailedLogin(
						middleware.ClientIP(r),
						auth.HashEmail(email),
						r.UserAgent())
					api.WriteProblem(w, api.ErrInvalidCredentials())
					return
				}
				s.issueSessionCookie(w, r, existing)
				if !existing.EmailVerified() {
					s.sendEmailVerification(r.Context(), r, existing)
				}
				writeLoginJSON(w, existing)
				return
			}
			email = strings.ReplaceAll(email, "\r", "")
			email = strings.ReplaceAll(email, "\n", "")
			s.log.Error("signup.create_account", "err", createErr, "email", email)
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				"internal_error", "Internal Error", "failed to create account"))
			return
		}
		phc, err := auth.Encode(password)
		if err != nil {
			// NewPassword already passed Validate; this is a
			// crypto/rand failure, not a user input bug.
			s.log.Error("signup.argon2id_encode", "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				"internal_error", "Internal Error", "failed to hash password"))
			return
		}
		if err := s.store.SetAccountPassword(r.Context(), created.ID, phc); err != nil {
			s.log.Error("signup.set_password", "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				"internal_error", "Internal Error", "failed to set password"))
			return
		}
		s.sendEmailVerification(r.Context(), r, created)
		s.issueSessionCookie(w, r, created)
		writeLoginJSON(w, created)
		return
	}

	// Email is bound. Two sub-cases:
	//   - Same password: sign in (idempotent retry).
	//   - Different password: 401 invalid_credentials (NEVER 409).
	hash, err := s.store.AccountPasswordByAccountID(r.Context(), acct.ID)
	if err != nil {
		// Bound account with no password row (OAuth-only): pad.
		_, _ = auth.Verify(auth.DummyPHC, password)
		// Issue #286: emit failed-login (OAuth-only branch).
		// Same payload shape as the postLoginEmail path so the
		// audit row is indistinguishable from the /login
		// wrong-password path — the discriminator is the email
		// hash, not the route.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			auth.HashEmail(email),
			r.UserAgent())
		api.WriteProblem(w, api.ErrInvalidCredentials())
		return
	}
	ok, err = auth.Verify(hash, password)
	if err != nil || !ok {
		// Issue #286: emit failed-login (wrong-password branch).
		// This is the canonical "wrong password" audit row, hit
		// by the bulk of credential-stuffing attempts. The
		// counter (`apid_failed_login_total{ip}`) is the
		// operational signal — the audit row in the events table
		// is the SOC 2 evidence.
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			auth.HashEmail(email),
			r.UserAgent())
		api.WriteProblem(w, api.ErrInvalidCredentials())
		return
	}
	s.issueSessionCookie(w, r, acct)
	if !acct.EmailVerified() {
		s.sendEmailVerification(r.Context(), r, acct)
	}
	writeLoginJSON(w, acct)
}

// postForgotPassword is the public POST /login/forgot. ALWAYS
// returns 200 with an identical body regardless of whether the email
// is bound, so the response cannot be used to enumerate accounts.
//
// The token is a 32-byte cryptographically random value; the server
// persists SHA-256(token) via IssueLoginToken with a 15-minute TTL,
// and the mailer carries the base64url-encoded plaintext to the
// customer. The plaintext never lands in the DB.
func (s *server) postForgotPassword(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(strings.ToLower(extractEmailFromRequest(r)))
	// Lookup may fail because the email is unbound, the store is
	// unreachable, or the email is empty. We don't care which — the
	// response is identical and the mailer only fires on a real
	// account hit.
	if email != "" && looksLikeEmail(email) {
		if acct, err := s.store.AccountByEmail(r.Context(), email); err == nil && acct.EmailVerified() {
			s.sendPasswordResetEmail(r.Context(), r, acct, email)
		}
	}
	// Always 200 with the same body. Anti-enumeration.
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// sendPasswordResetEmail mints a 32-byte token, persists SHA-256(token),
// and emails the base64url-encoded plaintext to the customer. Errors
// at any step are logged but never surface — the calling public
// endpoint remains a constant 200.
func (s *server) sendPasswordResetEmail(ctx context.Context, r *http.Request, acct state.Account, email string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("forgot_password.rand", "err", err)
		return
	}
	hash := api.HashToken(raw)
	expiresAt := time.Now().Add(passwordResetTTL)
	if err := s.store.IssueLoginToken(ctx, hash, acct.ID, expiresAt); err != nil {
		s.log.Error("forgot_password.issue_token", "err", err)
		return
	}
	scheme := schemeHTTP
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
		scheme = schemeHTTPS
	}
	host := r.Host
	if s.domain != "" && s.domain != domainUnset {
		// Use the configured domain verbatim — the email link should
		// always point at the canonical hostname, not the loopback
		// the request arrived on.
		host = s.domain
		scheme = schemeHTTPS
	}
	link := fmt.Sprintf("%s://%s%s?token=%s", scheme, host, resetTokenPath, base64.RawURLEncoding.EncodeToString(raw))
	body := "Hi,\n\nReset your faas password by clicking the link below (valid for 15 minutes):\n\n  " + link + "\n\nIf you did not request this, you can ignore this email.\n"
	subject := "Reset your faas password"
	if err := s.mailer.Send(ctx, Message{
		To:       []string{email},
		Subject:  subject,
		TextBody: body,
	}); err != nil {
		// Mailer failure is operator-visible but not customer-visible.
		s.log.Error("forgot_password.mailer", "err", err)
	}
}

// renderResetForm handles GET /auth/reset?token=…. Returns a 410
// Gone if the token is missing / malformed. The form template is
// rendered for valid-shape tokens; the actual consume happens on
// POST so a GET (preview) doesn't burn the token.
func (s *server) renderResetForm(w http.ResponseWriter, r *http.Request) {
	tok := strings.TrimSpace(r.URL.Query().Get("token"))
	if tok == "" {
		api.WriteProblem(w, api.ErrResetTokenInvalid())
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 32 {
		api.WriteProblem(w, api.ErrResetTokenInvalid())
		return
	}
	// We don't peek-consume on GET (the consume is the POST).
	// The form-side copy can render "this link is valid" without
	// burning the token. Bad token on POST is detected by
	// ConsumeLoginToken returning ErrNotFound.
	page := dashboard.Page{
		Title: "Reset password",
		Body:  "password_reset_form",
	}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.Error("dashboard render reset form", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// postReset handles POST /auth/reset. Consumes the token atomically
// (state.ConsumeLoginToken marks it consumed in one transaction so
// a replay returns 410), Argon2id-encodes the new password, calls
// SetAccountPassword, and signs the caller in.
func (s *server) postReset(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse form body"))
		return
	}
	tok := strings.TrimSpace(r.FormValue("token"))
	plain := r.FormValue("password")
	if tok == "" {
		api.WriteProblem(w, api.ErrResetTokenInvalid())
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 32 {
		api.WriteProblem(w, api.ErrResetTokenInvalid())
		return
	}
	if err := auth.Validate(plain); err != nil {
		api.WriteProblem(w, api.ErrPasswordTooWeak(err.Error()))
		return
	}
	hash := api.HashToken(raw)
	accountID, err := s.store.ConsumeLoginToken(r.Context(), hash)
	if err != nil {
		// Either unknown (already consumed / typo'd) or expired.
		// MemStore + PgStore both map past-TTL consume to
		// ErrNotFound today; the dashboard renders "invalid or
		// expired" copy that covers both. A future split into
		// ErrResetTokenInvalid vs ErrResetTokenExpired is a small
		// pgstore change.
		api.WriteProblem(w, api.ErrResetTokenInvalid())
		return
	}
	phc, err := auth.Encode(plain)
	if err != nil {
		s.log.Error("reset.argon2id_encode", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to hash password"))
		return
	}
	if err := s.store.SetAccountPassword(r.Context(), accountID, phc); err != nil {
		s.log.Error("reset.set_password", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to set password"))
		return
	}
	acct, err := s.store.AccountByID(r.Context(), accountID)
	if err != nil {
		s.log.Error("reset.account_lookup", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "account lookup failed"))
		return
	}
	s.issueSessionCookie(w, r, acct)
	http.Redirect(w, r, "/dashboard/", http.StatusFound)
}

// postSetPassword is the authenticated POST /dashboard/account/set-password.
// Behind sessionAuth; lets OAuth-only customers opt into password
// login and lets customers who already have one replace it. The same
// Argon2id Encode + SetAccountPassword path as reset, but anchored to
// the session's account rather than a reset token.
//
// ADR-140: the proof of presence is chosen by what the account has,
// in this order, instead of a blanket TOTP step-up on the mount:
//
//  1. A fresh step-up stamp (≤ setPasswordStepUpTTL) is accepted as-is —
//     unchanged for customers who verified TOTP moments ago.
//  2. MFARequired with no enrollment → 403 mfa_required: an explicit
//     account policy is pending completion.
//  3. MFA enrolled → 403 step_up_required: the customer has a second
//     factor and must use it, whether or not they also have a password.
//  4. The account has a password → `current_password` is required and
//     verified. Missing and wrong are the same 401 invalid_credentials
//     (the caller already knows the account exists, so there is nothing
//     to enumerate — the padding is for timing, not for presence).
//  5. No password, no MFA → accepted. There is no factor to re-verify;
//     the session is the only proof there is, and this opt-in is the
//     reason the route exists. The audit row records `proof=session`.
func (s *server) postSetPassword(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse form body"))
		return
	}
	// Same-site form POST: a function at *.apps.gregale.dev is same-site
	// with api.gregale.dev, so SameSite=Lax still sends faas_sid with a
	// form that page auto-submits. The purpose-bound token (minted by
	// GET /v1/auth/csrf?action=set_password) is what proves the form
	// came from the console — same guard as dashboardDelete.
	if err := middleware.VerifyAuthenticated(s.sessions, r, csrfActionSetPassword, acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	// The length rule is free and inspects only the new password, so it
	// runs before the proof: no DB read and no Argon2id verify for a
	// request that was never going to be accepted (pkg/auth/password.go
	// Validate). Nothing leaks — the caller is already the account.
	plain := r.FormValue("password")
	if err := auth.Validate(plain); err != nil {
		api.WriteProblem(w, api.ErrPasswordTooWeak(err.Error()))
		return
	}
	proof, replacing, ok := s.setPasswordProof(w, r, acct)
	if !ok {
		return
	}
	phc, err := auth.Encode(plain)
	if err != nil {
		s.log.Error("set_password.argon2id_encode", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to hash password"))
		return
	}
	if err := s.store.SetAccountPassword(r.Context(), acct.ID, phc); err != nil {
		s.log.Error("set_password.store", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to set password"))
		return
	}
	s.audit.Emit(r.Context(), "account.password_set", &acct.ID, map[string]any{
		"proof":    proof,
		"replaced": replacing,
	})
	http.Redirect(w, r, "/dashboard/account/", http.StatusFound)
}

// setPasswordStepUpTTL is the same 5-minute window ADR-077 uses on
// every other sensitive-op route.
const setPasswordStepUpTTL = 5 * time.Minute

// setPasswordProof decides whether the caller has shown enough to
// replace or set the account's password (ADR-140, matrix on
// postSetPassword). Returns the proof name for the audit row and
// whether a password already existed. On refusal it has written the
// problem and returns ok=false.
func (s *server) setPasswordProof(w http.ResponseWriter, r *http.Request, acct state.Account) (proof string, replacing bool, ok bool) {
	hash, err := s.store.AccountPasswordByAccountID(r.Context(), acct.ID)
	replacing = err == nil
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		s.log.Error("set_password.lookup", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to read password"))
		return "", false, false
	}

	ts, has := authmw.StepUpFrom(r)
	if has && !ts.IsZero() && time.Since(ts) <= setPasswordStepUpTTL {
		return "step_up", replacing, true
	}

	// MFA is opt-in for ordinary accounts: an account that has neither
	// enrolled nor been explicitly armed remains eligible for the
	// OAuth-only/session proof below. Preserve the separate
	// MFARequired policy hook, though; a pending enrollment policy must
	// not be bypassed merely because this dashboard route uses the
	// proof-selection handler instead of RequireMFA.
	if acct.MFARequired && !acct.MFAEnrolled() {
		s.audit.Emit(r.Context(), "auth.mfa_gate_hit", &acct.ID, map[string]any{
			"path":   setPasswordPath,
			"method": http.MethodPost,
		})
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeMFARequired,
			"MFA required", "complete /v1/account/mfa/enroll or /v1/account/mfa/confirm to access this route"))
		return "", replacing, false
	}

	// An enrolled second factor outranks the knowledge factor: a phished
	// password plus a stolen session must not be enough to rotate the
	// password on an account that has TOTP. This keeps MFA customers on
	// the ADR-077 tier regardless of whether they also have a password.
	if acct.MFAEnrolled() {
		// Same row RequireStepUpHandler emits, including its
		// missing/expired split — ADR-077's queries filter on it.
		reason := "missing"
		if has && !ts.IsZero() {
			reason = "expired"
		}
		// Path and method are the mount's constants, not read off the
		// request: the row is identical, and nothing request-derived
		// flows into the audit payload.
		s.audit.Emit(r.Context(), "auth.step_up_required", &acct.ID, map[string]any{
			"path":    setPasswordPath,
			"method":  http.MethodPost,
			"reason":  reason,
			"ttl_sec": int(setPasswordStepUpTTL.Seconds()),
		})
		api.WriteProblem(w, api.ErrStepUpRequired())
		return "", replacing, false
	}

	if replacing {
		current := r.FormValue("current_password")
		matched, verr := auth.Verify(hash, current)
		if verr != nil {
			// A malformed stored hash is a data problem, not a wrong
			// guess; Verify's contract is that it must be surfaced.
			// Still 401 to the caller — nothing else is safe to say.
			s.log.Error("set_password.verify", "account_id", acct.ID, "err", verr)
		}
		if verr != nil || !matched {
			s.audit.Emit(r.Context(), "account.password_set_denied", &acct.ID, map[string]any{
				"reason": "current_password",
			})
			api.WriteProblem(w, api.ErrInvalidCredentials())
			return "", true, false
		}
		return "current_password", true, true
	}

	return "session", false, true
}

// decodeEmailPasswordRequest pulls email + password out of either a
// JSON body or an x-www-form-urlencoded body. Returns ("", "", false)
// and writes the appropriate Problem on failure so the caller can
// just `return`.
func decodeEmailPasswordRequest(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	ct := r.Header.Get("Content-Type")
	email, password := "", ""
	if strings.HasPrefix(ct, "application/json") {
		var body api.PasswordLoginRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			api.WriteProblem(w, api.ErrValidation("could not decode JSON body"))
			return "", "", false
		}
		email, password = body.Email, body.Password
	} else {
		if err := r.ParseForm(); err != nil {
			api.WriteProblem(w, api.ErrValidation("could not parse form body"))
			return "", "", false
		}
		email, password = r.FormValue("email"), r.FormValue("password")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || !looksLikeEmail(email) {
		api.WriteProblem(w, api.ErrValidation("email is not a well-formed address"))
		return "", "", false
	}
	if password == "" {
		api.WriteProblem(w, api.ErrValidation("password is required"))
		return "", "", false
	}
	return email, password, true
}

// extractEmailFromRequest reads the email field from JSON or form
// bodies for the forgot-password path. The email is OPTIONAL on
// /login/forgot (the form-page version submits no body); an empty
// result is a valid no-op mailer-fires-never outcome.
func extractEmailFromRequest(r *http.Request) string {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		var body struct {
			Email string `json:"email"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		return body.Email
	}
	if err := r.ParseForm(); err == nil {
		return r.FormValue("email")
	}
	return ""
}

// verifyPasswordOrPad is the anti-enumeration pad. Returns the
// verified account on success, or (zero, false) on any failure
// mode. Every failure path runs ONE Argon2id verify against the
// same parameters: real hash on the "password row exists" path,
// DummyPHC on the no-row path. The branching is by necessity inside
// the function, but the work done is constant — the
// DummyPHC pad exists specifically to make the no-account /
// wrong-password / no-password-row paths identical in CPU cost.
func (s *server) verifyPasswordOrPad(ctx context.Context, email, password string) (state.Account, bool) {
	acct, err := s.store.AccountByEmail(ctx, email)
	if err != nil {
		// Email unbound. Run the Argon2id pad and return.
		_, _ = auth.Verify(auth.DummyPHC, password)
		return state.Account{}, false
	}
	hash, err := s.store.AccountPasswordByAccountID(ctx, acct.ID)
	if err != nil {
		// Bound account, no password row (OAuth-only). Pad.
		_, _ = auth.Verify(auth.DummyPHC, password)
		return state.Account{}, false
	}
	ok, err := auth.Verify(hash, password)
	if err != nil || !ok {
		return state.Account{}, false
	}
	return acct, true
}

// verifyOAuthAccountEmail records the address proof supplied by an OAuth
// provider. Fresh OAuth accounts already default verified; this closes the
// existing-password-account path when the customer later signs in through a
// provider with the same verified address.
func (s *server) verifyOAuthAccountEmail(ctx context.Context, acct state.Account) (state.Account, error) {
	if acct.EmailVerified() {
		return acct, nil
	}
	if err := s.store.MarkAccountEmailVerified(ctx, acct.ID); err != nil {
		return state.Account{}, err
	}
	verifiedAt := time.Now().UTC()
	acct.EmailVerifiedAt = &verifiedAt
	return acct, nil
}

// issueSessionCookie mints a session via the server's session.Manager
// and sets the HttpOnly + SameSite=Lax faas_sid cookie. The
// Secure flag is set when the request arrived via TLS or when the
// X-Forwarded-Proto header pins it (the loopback dev path is HTTP).
//
// IAM-2 (issue #186): the cookie is stamped with MfaPending=true
// when the account has opted into MFA (mfa_enrolled) or an explicit
// mfa_required policy is active. The requireMFA middleware
// (cmd/apid/mfa_middleware.go) reads the flag off the envelope via
// withMFAPending; every protected route 403s CodeMFARequired while
// the cookie is pending. The mfaSessionPending predicate is the
// same one used by the OAuth callbacks so all five cookie-issue
// paths agree on the policy.
//
// IAM-3 (ADR-039, issue #187 + #244 merged): the cookie is now
// issued via issueDashboardSession (cmd/apid/issue_session.go),
// which mints a sid, persists the sessions row, seals the
// envelope with the same sid, and emits auth.session.created.
// method = "password" — the only call site here is postLoginEmail
// (handlers_auth_login.go), the email+password ladder reserved
// for issue #2 / PR #2 once IAM-3 lands.
func (s *server) issueSessionCookie(w http.ResponseWriter, r *http.Request, acct state.Account) {
	cookie, _, err := s.issueDashboardSession(r.Context(), r, acct.ID, mfaSessionPending(acct), "password")
	if err != nil {
		s.log.Error("auth.session_issue", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to issue session"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionCookieLifetime.Seconds()),
	})
}

// writeLoginJSON writes the {account_id, plan} success body. NO
// api_key field — the pre-#165 takeover path returned the freshly
// minted key here, and we keep the body shape locked even on
// successful login.
func writeLoginJSON(w http.ResponseWriter, acct state.Account) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(api.PasswordLoginResponse{
		AccountID: acct.ID,
		Plan:      string(acct.Plan),
	})
}

// ---------------------------------------------------------------------------
// Programmatic auth surface (issue #311) — JSON-only, bearer-key CLI path.
// ---------------------------------------------------------------------------

// magicLinkPath is the verify URL the on-disk link points at. Kept
// distinct from resetTokenPath so future copy/branding can diverge
// without changing the wire shape.
const magicLinkPath = "/auth/verify"

// postV1AuthSignup is the JSON-only POST /v1/auth/signup. Returns a
// ProgrammaticAuthResponse{account_id, plan, api_key} on success so
// the gregale CLI can persist the plaintext via saveToken() without
// a dashboard round-trip.
//
// Anti-enumeration posture (spec §11):
//   - email unbound: create account, set password, mint api_key, return 200.
//   - email bound + same password: idempotent sign-in, mint a fresh
//     api_key, return 200.
//   - email bound + different password: pad Argon2id verify, emit
//     failed-login audit, return 401 invalid_credentials (NEVER 409 —
//     an attacker cannot enumerate via /v1/auth/signup).
//   - weak password: 400 password_too_weak (mirrors the postSignup path).
//
// The wire shape is identical to postV1AuthLogin so the CLI can reuse
// the same ProgrammaticAuthResponse unmarshaler.
func (s *server) postV1AuthSignup(w http.ResponseWriter, r *http.Request) {
	email, password, ok := decodeEmailPasswordRequest(w, r)
	if !ok {
		return
	}
	if err := auth.Validate(password); err != nil {
		api.WriteProblem(w, api.ErrPasswordTooWeak(err.Error()))
		return
	}

	// Look up the email up front — used by the create branch (line
	// ~613) to detect the unbound case and by verifyPasswordOrPad
	// in the bound branch (line ~669) to short-circuit the
	// AccountPasswordByAccountID read.
	_, lookupErr := s.store.AccountByEmail(r.Context(), email)
	if lookupErr != nil {
		// Email unbound — create the account, set the password, mint
		// a fresh api_key. Mirrors the postSignup race-closure: a
		// concurrent signup returns ErrConflict which we collapse to
		// the idempotent sign-in path so the duplicate caller never
		// learns "this email is taken".
		res, createErr := s.store.CreateAccountWithPersonalOrg(r.Context(), state.CreateAccountWithPersonalOrgParams{
			Email:                    email,
			Plan:                     api.PlanFree,
			RequireEmailVerification: true,
		})
		if createErr != nil {
			if errors.Is(createErr, state.ErrConflict) {
				existing, ok := s.verifyPasswordOrPad(r.Context(), email, password)
				if !ok {
					s.audit.EmitFailedLogin(
						middleware.ClientIP(r),
						auth.HashEmail(email),
						r.UserAgent())
					api.WriteProblem(w, api.ErrInvalidCredentials())
					return
				}
				if !existing.EmailVerified() {
					s.sendEmailVerification(r.Context(), r, existing)
				}
				s.mintAndWriteV1AuthJSON(w, r, existing, email)
				return
			}
			email = strings.ReplaceAll(email, "\r", "")
			email = strings.ReplaceAll(email, "\n", "")
			s.log.Error("v1auth_signup.create_account", "err", createErr, "email", email)
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				"internal_error", "Internal Error", "failed to create account"))
			return
		}
		created := res.Account
		phc, err := auth.Encode(password)
		if err != nil {
			s.log.Error("v1auth_signup.argon2id_encode", "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				"internal_error", "Internal Error", "failed to hash password"))
			return
		}
		if err := s.store.SetAccountPassword(r.Context(), created.ID, phc); err != nil {
			s.log.Error("v1auth_signup.set_password", "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
				"internal_error", "Internal Error", "failed to set password"))
			return
		}
		s.sendEmailVerification(r.Context(), r, created)
		s.mintAndWriteV1AuthJSON(w, r, created, email)
		return
	}

	// Email is bound. Funnel through verifyPasswordOrPad so the
	// Argon2id timing pad (spec §11) and the no-password-row branch
	// (OAuth-only accounts) stay aligned with postV1AuthLogin and
	// postLoginEmail. Otherwise the duplicate Argon2id-verify here
	// would drift from the canonical helper.
	existing, hit := s.verifyPasswordOrPad(r.Context(), email, password)
	if !hit {
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			auth.HashEmail(email),
			r.UserAgent())
		api.WriteProblem(w, api.ErrInvalidCredentials())
		return
	}
	if !existing.EmailVerified() {
		s.sendEmailVerification(r.Context(), r, existing)
	}
	s.mintAndWriteV1AuthJSON(w, r, existing, email)
}

// postV1AuthLogin is the JSON-only POST /v1/auth/login. Same response
// shape as postV1AuthSignup (ProgrammaticAuthResponse). Mirrors the
// anti-enumeration posture of postLoginEmail: Argon2id pad on the
// no-row branch, identical 401 on wrong-password vs unbound email.
func (s *server) postV1AuthLogin(w http.ResponseWriter, r *http.Request) {
	email, password, ok := decodeEmailPasswordRequest(w, r)
	if !ok {
		return
	}
	acct, hit := s.verifyPasswordOrPad(r.Context(), email, password)
	if !hit {
		s.audit.EmitFailedLogin(
			middleware.ClientIP(r),
			auth.HashEmail(email),
			r.UserAgent())
		api.WriteProblem(w, api.ErrInvalidCredentials())
		return
	}
	if !acct.EmailVerified() {
		s.sendEmailVerification(r.Context(), r, acct)
	}
	s.mintAndWriteV1AuthJSON(w, r, acct, email)
}

// postV1AuthSignupMagicLink is the JSON-only POST /v1/auth/signup/magic-link.
// ALWAYS returns 200 with an identical body regardless of whether the
// email is bound, so the response cannot be used to enumerate accounts.
// On a real-account hit, mint a 32-byte token, persist via
// IssueLoginToken with a 15-minute TTL, and email the
// /auth/verify?token=… link via the platform mailer.
func (s *server) postV1AuthSignupMagicLink(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(strings.ToLower(extractEmailFromRequest(r)))
	if email != "" && looksLikeEmail(email) {
		acct, err := s.store.AccountByEmail(r.Context(), email)
		if err != nil {
			// Unbound: create the account on the magic-link path so
			// the dashboard verify page lands the user on a logged-in
			// session for a fresh address. Mirrors the same shape as
			// postForgotPassword (mailer fires only on a real hit).
			res, createErr := s.store.CreateAccountWithPersonalOrg(r.Context(), state.CreateAccountWithPersonalOrgParams{
				Email:                    email,
				Plan:                     api.PlanFree,
				RequireEmailVerification: true,
			})
			if createErr == nil {
				acct = res.Account
			} else if errors.Is(createErr, state.ErrConflict) {
				// Concurrent signup race: a parallel request just
				// bound the email. Re-fetch so we still mint a
				// magic-link for the now-bound account. Mirrors the
				// conflict branch in postV1AuthSignup (line ~619).
				if existing, lookErr := s.store.AccountByEmail(r.Context(), email); lookErr == nil {
					acct = existing
				} else {
					safeEmail := strings.ReplaceAll(strings.ReplaceAll(email, "\r", ""), "\n", "")
					s.log.Error("v1auth_signup_magic.conflict_refetch", "err", lookErr, "email", safeEmail)
				}
			} else {
				safeEmail := strings.ReplaceAll(strings.ReplaceAll(email, "\r", ""), "\n", "")
				s.log.Error("v1auth_signup_magic.create_account", "err", createErr, "email", safeEmail)
			}
		}
		if acct.ID != "" {
			s.sendMagicLinkEmail(r.Context(), r, acct, email)
		}
	}
	// Always 200 with the same body. Anti-enumeration.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

// mintAndWriteV1AuthJSON mints a fresh programmatic api_key for acct
// and writes the ProgrammaticAuthResponse body. Mirrors the
// /v1/keys mint pattern (handlers_ext.go:1751): GenerateAPIKey →
// SHA-256 → CreateAPIKeyWithExpiryAndProvenance with provenance
// ("cli_signup", ip, ua), label "signup", expiry 0. The audit
// provenance columns (created_ip, created_ua) are stamped on the
// row so a SOC 2 auditor can answer "who minted this key from which
// UA" without joining through Loki (R2 risk).
func (s *server) mintAndWriteV1AuthJSON(w http.ResponseWriter, r *http.Request, acct state.Account, requestEmail string) {
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		s.log.Error("v1auth_signup.generate_key", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "could not generate api key"))
		return
	}
	bindIP := clientIPFromRequest(r)
	bindUA := r.UserAgent()
	org, perr := s.store.OrgByPersonalAccount(r.Context(), acct.ID)
	var k state.APIKey
	switch {
	case perr == nil:
		// Personal-org modern path. The label "signup" distinguishes
		// these rows from /v1/keys-add key-mints in the audit query.
		k, err = s.store.CreateOrgAPIKeyWithProvenance(r.Context(), org.ID, acct.ID, hash, "signup", nil, nil, bindIP, bindUA, nil)
	case errors.Is(perr, state.ErrNotFound):
		// Legacy fallback (pre-00127 fixtures). Same provenance columns.
		k, err = s.store.CreateAPIKeyWithExpiryAndProvenance(r.Context(), acct.ID, hash, "signup", nil, nil, bindIP, bindUA, nil)
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "could not resolve personal org"))
		return
	}
	if err != nil {
		s.log.Error("v1auth_signup.create_key", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "could not create api key"))
		return
	}
	writeV1AuthJSON(w, acct, requestEmail, plaintext, k)
}

// writeV1AuthJSON writes the ProgrammaticAuthResponse with the
// freshly-minted api_key payload. The plaintext is returned ONCE;
// the caller (gregale CLI) persists via saveToken() before this
// response is dropped.
// writeV1AuthJSON writes the ProgrammaticAuthResponse with the
// freshly-minted api_key payload. The plaintext is returned ONCE;
// the caller (gregale CLI) persists via saveToken() before this
// response is dropped.
//
// requestEmail is the email the client sent in the POST body —
// the CLI needs it for "Logged in as <email>" (issue #311 R1
// fix: empty Email made the success line render as
// "Logged in as  (free plan)"). state.Account.Email would work
// too, but threading the request value keeps the writer
// signature stable for the conflict branch where acct may
// already be populated from the verifyPasswordOrPad path.
func writeV1AuthJSON(w http.ResponseWriter, acct state.Account, requestEmail, plaintext string, k state.APIKey) {
	prefix := api.APIKeyPrefix
	if len(plaintext) >= len(api.APIKeyPrefix)+8 {
		prefix = plaintext[:len(api.APIKeyPrefix)+8]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(api.ProgrammaticAuthResponse{
		AccountID: acct.ID,
		Email:     requestEmail,
		Plan:      string(acct.Plan),
		APIKey: api.ProgrammaticAPIKey{
			Plaintext: plaintext,
			Prefix:    prefix,
			ID:        k.ID,
		},
	})
}

// sendMagicLinkEmail is the magic-link sibling of sendPasswordResetEmail:
// mints a 32-byte token, persists SHA-256(token) via IssueLoginToken
// with a 15-minute TTL, and emails the base64url-encoded plaintext
// via the platform mailer. Mailer errors are logged but never
// surface — the public endpoint remains a constant 200.
func (s *server) sendMagicLinkEmail(ctx context.Context, r *http.Request, acct state.Account, email string) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("v1auth_signup_magic.rand", "err", err)
		return
	}
	hash := api.HashToken(raw)
	expiresAt := time.Now().Add(passwordResetTTL)
	if err := s.store.IssueLoginToken(ctx, hash, acct.ID, expiresAt); err != nil {
		s.log.Error("v1auth_signup_magic.issue_token", "err", err)
		return
	}
	scheme := schemeHTTP
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
		scheme = schemeHTTPS
	}
	host := r.Host
	if s.domain != "" && s.domain != domainUnset {
		host = s.domain
		scheme = schemeHTTPS
	}
	link := fmt.Sprintf("%s://%s%s?token=%s", scheme, host, magicLinkPath, base64.RawURLEncoding.EncodeToString(raw))
	body := fmt.Sprintf(
		"Hi,\n\nWelcome to faas. Confirm your email by clicking the link below (valid for 15 minutes):\n\n  %s\n\nIf you did not request this, you can ignore this email.\n",
		link)
	subject := "Confirm your faas account"
	if err := s.mailer.Send(ctx, Message{
		To:       []string{email},
		Subject:  subject,
		TextBody: body,
	}); err != nil {
		s.log.Error("v1auth_signup_magic.mailer", "err", err)
	}
}

// sendEmailVerification mints a 24-hour, single-use address-verification
// token. Delivery errors remain server-side because signup has already
// committed; a same-credential signup retry mints and sends a fresh link.
func (s *server) sendEmailVerification(ctx context.Context, r *http.Request, acct state.Account) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("email_verification.rand", "err", err)
		return
	}
	hash := api.HashToken(raw)
	expiresAt := time.Now().Add(emailVerificationTTL)
	if err := s.store.IssueEmailVerificationToken(ctx, hash, acct.ID, expiresAt); err != nil {
		s.log.Error("email_verification.issue_token", "err", err, "account_id", acct.ID)
		return
	}
	scheme := schemeHTTP
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == schemeHTTPS {
		scheme = schemeHTTPS
	}
	host := r.Host
	if s.domain != "" && s.domain != domainUnset {
		host = s.domain
		scheme = schemeHTTPS
	}
	link := fmt.Sprintf("%s://%s%s?token=%s", scheme, host, emailVerificationPath, base64.RawURLEncoding.EncodeToString(raw))
	subject, body := mailpkg.EmailVerificationBody(acct.Email, link, expiresAt)
	if err := s.mailer.Send(ctx, Message{
		To:       []string{acct.Email},
		Subject:  subject,
		TextBody: body,
	}); err != nil {
		s.log.Error("email_verification.mailer", "err", err, "account_id", acct.ID)
	}
}

// verifyEmail consumes a verification token without authenticating the
// browser. The success page explicitly sends the customer back through sign
// in, so possession of the email link never becomes a session credential.
func (s *server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		http.Error(w, "verification link expired or already used", http.StatusGone)
		return
	}
	accountID, err := s.store.ConsumeEmailVerificationToken(r.Context(), api.HashToken(raw))
	if err != nil {
		s.log.Info("email_verification.invalid_token", "err", err)
		http.Error(w, "verification link expired or already used", http.StatusGone)
		return
	}
	s.audit.Emit(r.Context(), "auth.email_verified", &accountID, map[string]any{
		"method": "email_link",
	})
	page := dashboard.Page{Title: "Email verified", Body: "email_verified"}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		s.log.Error("dashboard render email verified", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// Keep the imports tidy — these helpers are referenced by the
// future test surface and the README expansion; the imports stay
// green so gofmt doesn't flag them on a future edit.
