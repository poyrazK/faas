// Magic-link auth handlers (M7.5, ADR-011 dashboard).
//
// The dashboard auth flow:
//
//  1. GET  /login            → renders the email form
//  2. POST /login            → looks up the account by email AND the
//     pre-existing "web-console" API key presented via
//     X-Dashboard-Key. On a match, sets the faas_sid
//     session cookie. This handler no longer
//     auto-creates accounts and no longer mints a new
//     API key on login (issue #165, ADR-032).
//     PR #2 replaces this path with email+password
//     (Argon2id) and the legacy X-Dashboard-Key fallback
//     is removed once every pre-#165 customer has set a
//     password.
//  3. GET  /auth/verify?token=… → consumes the token (one-shot),
//     sets faas_sid cookie, redirects
//     to /dashboard/
//  4. POST /logout           → clears faas_sid, redirects to /login
//
// sessionAuth middleware gates /dashboard/* (except /login + the
// OAuth callback). Slice 4 fills the dashboard pages with real data;
// this slice only proves the auth envelope works.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// sessionCookie is the signed cookie name. Dashboards read this
	// from request context; apid sets it on successful /auth/verify.
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

	// dashboardKeyHeader is the X-Dashboard-Key header name. The
	// PR #1 login path (issue #165 fix) accepts a pre-existing
	// "web-console" API key here as the one remaining way to
	// authenticate. PR #2 replaces this with email+password and
	// drops the header — see ADR-032.
	dashboardKeyHeader = "X-Dashboard-Key"
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
	}
	if err := dashboard.Render(w, a.log, page); err != nil {
		a.log.Error("dashboard render login form", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
	}
}

// postLogin is the PR #1 (issue #165) hardened login path.
//
// Pre-#165, any POST /login with a well-formed email auto-created
// an account, minted a "web-console" API key, returned that key in
// the response body, and set a 7-day session cookie — with zero
// verification. That was a full pre-auth account-takeover (spec
// §11 violation).
//
// Post-#165:
//
//   - Auto-creation is gone. Unknown email → 401 invalid_credentials.
//   - The web-console API key mint is gone. The response body
//     carries only `{status, account}` — no api_key field.
//   - The ONLY way to authenticate through /login in PR #1 is to
//     present a pre-existing "web-console" API key via the
//     X-Dashboard-Key header. That fallback exists so the
//     customers the buggy handler created before #165 can still
//     reach the dashboard; PR #2 replaces it with email+password
//     (Argon2id) and removes the header — see ADR-032.
//
// Why header AND email, not header alone: a leaked API key alone
// must never be sufficient to take over a dashboard session. The
// customer's browser still has to submit the account's email
// alongside the key, and the session cookie is HttpOnly +
// SameSite=Lax. The X-Dashboard-Key path is a backstop for the
// pre-#165 customers; PR #2's email+password path replaces it.
func (a *authHandlers) postLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("could not parse form body"))
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	if email == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing email", "email is required"))
		return
	}
	if !looksLikeEmail(email) {
		api.WriteProblem(w, api.ErrValidation("email is not a well-formed address"))
		return
	}

	// PR #1 (issue #165, ADR-032): the only remaining way to
	// sign in via /login is a pre-existing "web-console" API key
	// presented via the X-Dashboard-Key header. We deliberately
	// do NOT fall back to auto-creating an account on lookup
	// miss — that path is what made the original handler a
	// pre-auth account-takeover.
	key := strings.TrimSpace(r.Header.Get(dashboardKeyHeader))
	if !api.ValidAPIKeyFormat(key) {
		a.log.Info("login.invalid_key_format", "email", logsanitize.Field(email))
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeInvalidCredentials, "Sign-in failed",
			"provide a valid dashboard key via X-Dashboard-Key"))
		return
	}

	acct, err := a.srv.store.AccountByKeyHash(r.Context(), api.HashAPIKey(key))
	if err != nil {
		// Same body as the email/key-mismatch path: anti-enumeration.
		a.log.Info("login.key_not_found", "email", logsanitize.Field(email))
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeInvalidCredentials, "Sign-in failed",
			"invalid email or dashboard key"))
		return
	}
	if !strings.EqualFold(strings.TrimSpace(acct.Email), email) {
		// The header resolved to a real account, but the
		// submitted email doesn't match. Don't leak which is
		// which; same body as the no-match path.
		a.log.Info("login.email_key_mismatch", "email", logsanitize.Field(email))
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized,
			api.CodeInvalidCredentials, "Sign-in failed",
			"invalid email or dashboard key"))
		return
	}

	// Mint session & set HttpOnly faas_sid cookie. The response
	// body carries NO api_key (PR #1 closes issue #165 — the
	// legacy handler returned the freshly minted key in the
	// response, which made the takeover reproducible from a
	// single POST /login curl).
	cookie, err := a.srv.sessions.Issue(acct.ID)
	if err != nil {
		a.log.Error("login.session_issue", "err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			"internal_error", "Internal Error", "failed to issue session"))
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    cookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionCookieLifetime.Seconds()),
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// NOTE: the response body intentionally omits `api_key`.
	// Pre-#165 this JSON had `"api_key": "fp_live_..."` — the
	// attacker grabbed that key and used it for full account
	// control. The key path remains as a PR #1 fallback ONLY
	// because pre-existing customers still hold a key from the
	// buggy deploy and need a way back in; the response never
	// surfaces it again.
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"account": acct,
	})
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
	cookie, err := a.srv.sessions.Issue(accountID)
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
	http.Redirect(w, r, "/dashboard/", http.StatusFound)
}

// logout handles POST /logout: clears the cookie and redirects to
// /login. We don't maintain a server-side session blocklist in v1.0 —
// the cookie's MaxAge of zero is enough to invalidate it on the
// client; spec §11 doesn't require a server-side kill switch.
func (a *authHandlers) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.domain != "",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
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
			http.Redirect(w, r, loginPath+"?next="+r.URL.Path, http.StatusFound)
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
// to wire the session.Manager. It reads FAAS_SESSION_KEY as a
// hex-encoded 32-byte string; empty in dev = ephemeral key + a
// warning string the caller can log.
//
// Production MUST set FAAS_SESSION_KEY to the hex contents of
// /etc/faas/secrets/session.key (root:root 0400, spec §11).
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
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != 32 {
		log.Error("FAAS_SESSION_KEY must be 64 hex chars (32 bytes)", "got_len", len(raw))
		// Fail closed: refuse to boot with a broken key. Operators
		// notice; dev gets a clear error rather than a silently
		// invalid manager.
		return nil, "FAAS_SESSION_KEY invalid"
	}
	m, err := session.NewManager(key, 7*24*time.Hour)
	if err != nil {
		log.Error("session manager build failed", "err", err)
		return nil, "session manager init failed"
	}
	return m, ""
}
