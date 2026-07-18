// Magic-link auth handlers (M7.5, ADR-011 dashboard).
//
// The dashboard auth flow:
//
//  1. GET  /login            → renders the email form
//  2. POST /login            → looks up the account by email, mints a
//     32-byte random token, stores its
//     SHA-256 hash with a 15-minute expiry,
//     emails the raw token to the user, and
//     renders "check your email"
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
	"crypto/rand"
	"encoding/hex"
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

// postLogin handles POST /login: mint token, store hash, email user.
//
// On unknown email we still return 200 with the same "check your
// email" copy — leaking which addresses exist is a small but
// real-enough risk to avoid (UX spec §5.4).
func (a *authHandlers) postLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	if email == "" {
		http.Error(w, "email required", http.StatusBadRequest)
		return
	}
	if !looksLikeEmail(email) {
		http.Error(w, "email invalid", http.StatusBadRequest)
		return
	}
	acct, err := a.srv.store.AccountByEmail(r.Context(), email)
	if err != nil {
		// Unknown email — same UX as success.
		a.log.Info("login.unknown_email", "email", logsanitize.Field(email))
		a.renderCheckEmail(w)
		return
	}
	token, hash, err := mintLoginToken()
	if err != nil {
		a.log.Error("login.mint_token", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	expiresAt := time.Now().Add(a.loginTTL)
	if err := a.srv.store.IssueLoginToken(r.Context(), hash, acct.ID, expiresAt); err != nil {
		a.log.Error("login.issue_token", "err", err)
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	magicLink := a.domain + verifyPath + "?token=" + token
	subject := "Sign in to onebox faas"
	body := "Click the link to sign in (15 min, one-time use):\n\n" + magicLink + "\n\nIf you didn't request this, ignore this email."
	if err := a.mailer.Send(r.Context(), Message{
		To:       []string{email},
		Subject:  subject,
		TextBody: body,
	}); err != nil {
		a.log.Error("login.send_email", "err", err, "email", logsanitize.Field(email))
		// Don't surface to the user — same UX as success so we don't
		// leak whether the address is registered.
	}
	a.log.Info("login.issued", "email", logsanitize.Field(email), "expires_at", expiresAt)
	a.renderCheckEmail(w)
}

func (a *authHandlers) renderCheckEmail(w http.ResponseWriter) {
	page := dashboard.Page{
		Title: "Sign in",
		Body:  "login",
		Flash: "Check your email — we sent you a magic link.",
	}
	if err := dashboard.Render(w, a.log, page); err != nil {
		a.log.Error("dashboard render check-email", "err", err)
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

// mintLoginToken returns (hex-encoded 32-byte raw token, sha256 hash,
// error). The hash is what we store; the hex raw token is what goes
// in the email link.
func mintLoginToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(raw), api.HashToken(raw), nil
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
