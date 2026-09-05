// Magic-link auth handlers (M7.5, ADR-011 dashboard).
//
// The dashboard auth flow as of PR #2 (issue #165, ADR-032):
//
//  1. GET  /login            → renders the email + password form
//     (and the Google / GitHub OAuth links).
//  2. POST /login            → Argon2id verify against the
//     account_passwords row. Sets the faas_sid cookie on success.
//     The response body is {account_id, plan} — no api_key field.
//  3. POST /signup           → create account + set password + sign
//     in (idempotent on the same email + password).
//  4. POST /login/forgot     → mint a 15-min reset token, email
//     the URL. Always 200 with the same body.
//  5. GET  /auth/reset       → render the reset form on a valid
//     token; 410 Gone on invalid / expired / consumed.
//  6. POST /auth/reset       → consume the token, set the new
//     password, sign the caller in.
//  7. GET  /v1/auth/google   → 302 to Google consent screen.
//  8. GET  /v1/auth/google/callback → state verify + code
//     exchange + email_verified enforcement + sign-in.
//  9. GET  /v1/auth/github   → 302 to GitHub consent screen.
//  10. GET /v1/auth/github/callback → state verify + code
//     exchange + primary-verified email filter + sign-in.
//  11. POST /dashboard/account/set-password → authed opt-in for
//     OAuth-only customers.
//  12. GET  /auth/verify?token=… → legacy magic-link consume
//     (kept for compatibility; PR #2 does not return new
//     magic-link emails).
//  13. POST /logout          → clears faas_sid, redirects to /login.
//
// sessionAuth middleware gates /dashboard/* (except /login + the
// OAuth callbacks). The X-Dashboard-Key header fallback that PR #1
// shipped has been removed in this PR (issue #165 PR #2 sweep gate).
package main

import (
	"context"
	"encoding/hex"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// sessionCookie is the signed cookie name. Dashboards read this
	// from request context; apid sets it on successful /login,
	// /auth/reset, and /v1/auth/* callback.
	sessionCookie = "faas_sid"
	// sessionCookieLifetime is the lifetime baked into the test
	// session manager. 7 days matches the production default in
	// loadSessionManager + newServerWithDeps.
	sessionCookieLifetime = 7 * 24 * time.Hour
	// loginPath + verifyPath + logoutPath are the public auth
	// surface. None of them sit behind sessionAuth.
	loginPath  = "/login"
	verifyPath = "/auth/verify"
	logoutPath = "/logout"
)

// authHandlers groups the dashboard-side auth dependencies so we can
// pass them around without changing the server struct just for slice 3.
type authHandlers struct {
	srv      *server
	log      *slog.Logger
	loginTTL time.Duration
	mailer   Mailer
	domain   string // base URL for the magic-link (e.g. https://faas.example.test)
}

// renderLoginForm renders the GET /login page.
func (a *authHandlers) renderLoginForm(w http.ResponseWriter, r *http.Request) {
	page := dashboard.Page{
		Title: "Sign in",
		Body:  "login",
		// Issue #419 / ADR-046: gate the OAuth buttons on the
		// boot-resolved provider state. With both vars unset the
		// buttons render as nothing (no 500-bound links); the
		// SignInConfig is read here — not at request time — so
		// there is no per-request os.Getenv cost.
		Auth: &dashboard.AuthCapabilitiesView{
			GoogleEnabled: a.srv.oauthConfig.Google.Enabled(),
			GitHubEnabled: a.srv.oauthConfig.GitHub.Enabled(),
		},
	}
	if err := dashboard.Render(w, a.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		a.log.Error("dashboard render login form", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// verify handles GET /auth/verify?token=…. On success, sets the
// faas_sid cookie and redirects to /dashboard/. On replay / expiry /
// invalid, returns 410 Gone (semantically correct: the resource was
// consumed). No information leak between cases — they all return 410.
func (a *authHandlers) verify(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	raw, err := hex.DecodeString(token)
	if err != nil || len(raw) != 32 {
		http.Error(w, "invalid token", http.StatusGone)
		return
	}
	hash := api.HashToken(raw) // SHA-256 of the raw 32 bytes
	accountID, err := a.srv.store.ConsumeLoginToken(r.Context(), hash)
	if err != nil {
		a.log.Info("auth.verify.invalid_token", "err", err)
		http.Error(w, "link expired or already used", http.StatusGone)
		return
	}
	// IAM-2 (issue #186): fetch the account so we can stamp the
	// mfa-pending flag on the cookie when the policy requires it.
	// A missing account is a token-mismatch race (the row was
	// hard-deleted between token mint and verify); treat as 410
	// Gone, matching the expired-token path above.
	acct, err := a.srv.store.AccountByID(r.Context(), accountID)
	if err != nil {
		a.log.Info("auth.verify.account_missing", "err", err, "account", accountID)
		http.Error(w, "link expired or already used", http.StatusGone)
		return
	}
	// The magic link was delivered to acct.Email, so consuming it also
	// proves address ownership. This leaves OAuth behavior unchanged and
	// avoids sending passwordless customers through a second email loop.
	if err := a.srv.store.MarkAccountEmailVerified(r.Context(), accountID); err != nil {
		a.log.Error("auth.verify.mark_email_verified", "err", err, "account", accountID)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	mfaPending := mfaSessionPending(acct)
	// IAM-3 (ADR-039): the magic-link verify path now mints a sid
	// + creates the sessions row + emits auth.session.created
	// through the unified helper. The route never reaches the
	// dashboardAuthChain (it lives outside s.auth) so this is the
	// only authoritative stamp the verify path leaves.
	cookie, _, err := a.srv.issueDashboardSession(r.Context(), r, accountID, mfaPending, "magic_link")
	if err != nil {
		a.log.Error("auth.verify.issue_session", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.domain != "", // Secure flag on when we have a real domain
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(a.srv.sessions.MaxAge().Seconds()),
	})
	// IAM-4 (ADR-035): record the magic-link login success.
	// IAM-2: extend the data shape with mfa_pending so the
	// dashboard audit list can pivot "which logins landed while
	// the account was required but unenrolled?".
	a.srv.audit.Emit(r.Context(), "auth.login", &accountID, map[string]any{
		"method":      "magic_link",
		"mfa_pending": mfaPending,
	})
	http.Redirect(w, r, "/dashboard/", http.StatusFound)
}

// logout handles POST /logout: clears the cookie and redirects to
// /login. We don't maintain a server-side session blocklist in v1.0 —
// the cookie's MaxAge of zero is enough to invalidate it on the
// client; spec §11 doesn't require a server-side kill switch.
func (a *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	// IAM-4 (ADR-035): the logout handler isn't behind sessionAuth,
	// so we resolve the account from the cookie inline so the audit
	// row can carry subject=account_id. If the cookie is missing
	// or invalid we still clear it (best-effort UX) and skip the
	// emit — there's no account to attribute the action to.
	var accountID string
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if env, err := a.srv.sessions.Verify(c.Value); err == nil {
			if acct, err := a.srv.store.AccountByID(r.Context(), env.AccountID); err == nil && acct.Active() {
				accountID = acct.ID
			}
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	// Emit before the redirect so the audit row is durable even if
	// the client closes the connection on 302.
	if accountID != "" {
		a.srv.audit.Emit(r.Context(), "auth.logout", &accountID, nil)
	}
	http.Redirect(w, r, loginPath, http.StatusFound)
}

// sessionAuth is the dashboard middleware. It reads faas_sid,
// verifies via session.Manager, looks up the account, and stashes
// both on the request context for downstream handlers to consume.
//
// Failure modes:
//   - no cookie / malformed cookie → 302 to /login?next=…
//     (keeps the URL as the redirect target post-login)
//   - cookie present but expired/tampered → 302 to /login + clear cookie
//   - account not found / suspended → 302 to /login (rare; means the
//     account was deleted while a session was live — don't leak which)
func (s *server) sessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			http.Redirect(w, r, loginPath+"?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		env, err := s.sessions.Verify(c.Value)
		if err != nil {
			// Clear the bad cookie.
			http.SetCookie(w, &http.Cookie{
				Name:     sessionCookie,
				Value:    "",
				Path:     "/",
				HttpOnly: true,
				Secure:   true, // session cookie is always HTTPS-only (issue paths at line 167/189 set this too)
				SameSite: http.SameSiteLaxMode,
				MaxAge:   -1,
			})
			http.Redirect(w, r, loginPath, http.StatusFound)
			return
		}
		acct, err := s.store.AccountByID(r.Context(), env.AccountID)
		if err != nil || !acct.Active() {
			http.Redirect(w, r, loginPath, http.StatusFound)
			return
		}
		r = r.WithContext(WithAccount(r.Context(), acct))
		// IAM-hardening-mega-PR (logical change 6, ADR-077 /
		// review finding #3): stamp env.StepUpAt onto
		// r.Context() so requireStepUpHandler can read it. The
		// dashboard mount sessionAuth → requireStepUpHandler(5m)
		// (server.go set-password / delete-account) was silently
		// bypassing the gate because sessionAuth never called
		// WithStepUp — StepUpFrom returned (zero, false), the
		// `!has` branch at middleware.go:889 forwarded the
		// request, and the "stolen browser, post-MFA-clear"
		// threat change 6 exists to close re-opened. env.StepUpAt
		// is zero for pre-PR-077 cookies (omitempty wire shape);
		// the gate sees ts.IsZero()==true and emits
		// reason="missing" + 403, forcing a step-up.
		//
		//nolint:contextcheck // pointer-mutation contract (same as withSession/withPrincipal/withMFAPending at middleware.go:530-554): r.Context() must be the inherited ctx; capturing into a local would break observeWrap.
		r = r.WithContext(authmw.WithStepUp(r.Context(), env.StepUpAt))
		next.ServeHTTP(w, r)
	})
}

// accountContextKey is the request-context key for the authenticated
// account (under sessionAuth). Avoids stringly-typed context values.
type accountContextKey struct{}

// WithAccount returns ctx with acct stashed under the sessionAuth key.
func WithAccount(ctx context.Context, acct state.Account) context.Context {
	return context.WithValue(ctx, accountContextKey{}, acct)
}

// AccountFrom extracts the authenticated account (or nil). Used by
// dashboard handlers in slice 4+.
func AccountFrom(ctx context.Context) (state.Account, bool) {
	a, ok := ctx.Value(accountContextKey{}).(state.Account)
	return a, ok
}

// looksLikeEmail is a permissive shape check — RFC 5322 is too
// permissive for a real validator; this catches the common
// "did you forget the @domain" mistakes without rejecting
// legitimate edge-case addresses.
func looksLikeEmail(s string) bool {
	if len(s) < 3 || len(s) > 254 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	if strings.IndexByte(s[at+1:], '.') < 0 {
		return false
	}
	return true
}

// context import — guard against removal when this file's only
// consumer switches to a different ctx source.
var _ = context.Background

// loadSessionManager is the boot-time helper cmd/apid/main.go uses
// to wire the session.Manager. It reads FAAS_SESSION_KEY under one of
// two shapes — see ADR-127 / env-var-shape-content-vs-path-systemd-
// loadcredential memory for the canonical rule:
//
//   - **PATH-shaped** (production default): the env var holds a
//     tmpfs path produced by systemd `LoadCredential=faas_session_key`
//
//   - `Environment=FAAS_SESSION_KEY=%d/faas_session_key`. systemd
//     copies /etc/faas/secrets/session.key (root:root 0400, spec §11)
//     to $CREDENTIALS_DIRECTORY and exposes the tmpfs copy via %d/.
//     We os.ReadFile the path, then hex-decode the file content.
//
//   - **CONTENT-shaped** (e2e tests, dev, operator muscle memory):
//     the env var holds the raw 64-hex-char string directly.
//     Compatible with the historical "FAAS_SESSION_KEY=<hex>" form
//     in /etc/faas/sealed.env (still legal for operators who don't
//     migrate to LoadCredential — but verify-secrets.sh asserts this
//     shape is NOT in sealed.env on production boxes; see
//     deploy/scripts/verify-secrets.sh:39-50).
//
// If the env value starts with '/' AND stat()s to a regular file,
// PATH-shaped wins (the env var IS the path). Otherwise we treat the
// value as raw content.
//
// Empty value → ephemeral manager + warning (dev fallback).
// Both shapes must produce a 32-byte hex key or the loader refuses to
// boot. Fail-closed: a broken key surfaces as a log.Error rather than
// a silent fallback to ephemeral mode (the A5 silent-degradation bug
// that motivated issue #585 / ADR-127 review-fix R1+R2).
func loadSessionManager(getenv func(string) string, log *slog.Logger) (*session.Manager, string) {
	raw := strings.TrimSpace(getenv("FAAS_SESSION_KEY"))
	if raw == "" {
		m, err := session.NewEphemeralManager(7 * 24 * time.Hour)
		if err != nil {
			log.Error("session ephemeral manager failed", "err", err)
			return nil, "ephemeral key failed"
		}
		return m, "FAAS_SESSION_KEY unset; ephemeral key in use"
	}
	// Shape detection: a leading "/" plus an existing regular file is
	// the canonical PATH-shaped contract (systemd %d/faas_session_key
	// resolves to $CREDENTIALS_DIRECTORY/faas_session_key, owned by
	// the unit's uid, mode 0600). Anything else (a hex string, a
	// missing file, a non-path value) falls through to the
	// CONTENT-shaped decoder for back-compat.
	if strings.HasPrefix(raw, "/") {
		if info, err := os.Stat(raw); err == nil && info.Mode().IsRegular() {
			// SECURITY: capture the path into a local BEFORE the
			// ReadFile/TrimSpace reassignment — otherwise the slog
			// log below would emit the hex key into the 'path' field
			// (CLAUDE.md "Never log secret values"; slog ships to
			// journald / Loki, where it would persist as a
			// credentials-shaped datum).
			path := raw
			data, readErr := os.ReadFile(raw)
			if readErr != nil {
				log.Error("FAAS_SESSION_KEY path read failed",
					"path", path, "err", readErr)
				return nil, "FAAS_SESSION_KEY path read failed"
			}
			raw = strings.TrimSpace(string(data))
			log.Info("FAAS_SESSION_KEY loaded via LoadCredential path",
				"path", path, "mode", info.Mode().String())
		}
	}
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		log.Error("FAAS_SESSION_KEY must be 64 hex chars (32 bytes)",
			"got_len", len(raw), "hex_err", decodeErr(err))
		// Fail closed: refuse to boot with a broken key. Operators
		// notice; dev gets a clear error rather than a silently
		// invalid manager. The previous version logged got_len but
		// swallowed the underlying parse error which made "FAAS_SESSION_KEY
		// is set to a path" indistinguishable from a "wrong-byte-length
		// truncation" in the operator runbook.
		return nil, "FAAS_SESSION_KEY invalid"
	}
	m, err := session.NewManager(key, 7*24*time.Hour)
	if err != nil {
		log.Error("session manager build failed", "err", err)
		return nil, "session manager init failed"
	}
	return m, ""
}

// decodeErr returns a short error string for the slog log; nil means
// "no error to decode". Lifted out so the call site reads cleaner
// against the multi-source raw (path-read vs env-direct).
func decodeErr(err error) string {
	if err == nil {
		return ""
	}
	// Truncate to keep the JSON log line bounded; the operator can
	// always re-run with `journalctl -u faas-apid -o cat` to see the
	// full hex error.
	if len(err.Error()) > 120 {
		return err.Error()[:120] + "..."
	}
	return err.Error()
}
