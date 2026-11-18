package apidserver

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/onebox-faas/faas/pkg/state"
)

// HTML GETs show the confirmation or recovery page without spending the
// token. Keep the existing one-step behavior for non-HTML clients.
func (s *server) verifyEmailConfirm(w http.ResponseWriter, r *http.Request) {
	s.handleEmailVerification(w, r, strings.TrimSpace(r.URL.Query().Get("token")), r.Method == http.MethodGet && !api.AcceptsHTML(r))
}

func (s *server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleEmailVerification(w, r, "", true)
		return
	}
	s.handleEmailVerification(w, r, strings.TrimSpace(r.PostFormValue("token")), true)
}

// The link proves mailbox ownership. Only the account's own live browser
// session can consume it (ADR 20261109120000).
func (s *server) handleEmailVerification(w http.ResponseWriter, r *http.Request, token string, confirm bool) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Add("Vary", "Accept")
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != api.EmailVerificationTokenBytes {
		s.invalidEmailVerification(w, r)
		return
	}
	hash := api.HashToken(raw)
	if !s.verificationFromAccountBrowser(w, r, hash, token) {
		return
	}
	if !confirm {
		s.renderEmailVerification(w, r, http.StatusOK, "Verify your email address", map[string]any{"Token": token})
		return
	}
	accountID, err := s.store.ConsumeEmailVerificationToken(r.Context(), hash)
	if err != nil {
		s.log.Info("email_verification.invalid_token", "err", err)
		s.invalidEmailVerification(w, r)
		return
	}
	s.audit.Emit(r.Context(), "auth.email_verified", &accountID, map[string]any{"method": "email_link"})
	s.renderEmailVerification(w, r, http.StatusOK, "Email verified", map[string]any{"Verified": true})
}

func (s *server) verificationFromAccountBrowser(w http.ResponseWriter, r *http.Request, hash []byte, token string) bool {
	peek, ok := s.store.(state.EmailVerificationPeekStore)
	if !ok {
		return true
	}
	ownerID, err := peek.EmailVerificationTokenAccount(r.Context(), hash)
	if err != nil {
		s.invalidEmailVerification(w, r)
		return false
	}
	browser, signedIn := s.verificationBrowserAccount(r)
	if signedIn && browser.ID == ownerID {
		return true
	}
	if !api.AcceptsHTML(r) {
		api.WriteProblemForRequest(w, r, api.NewProblem(http.StatusUnauthorized, api.CodeSessionExpired,
			"Sign in to verify", "open the link in a browser signed in to the account it was sent for"))
		return false
	}
	title := "Sign in to verify your email"
	if signedIn {
		title = "This link is for a different account"
	}
	next := emailVerificationPath + "?token=" + url.QueryEscape(token)
	s.renderEmailVerification(w, r, http.StatusOK, title, map[string]any{
		"SignInURL":    verificationSignInURL(next),
		"WrongAccount": signedIn,
	})
	return false
}

func (s *server) verificationBrowserAccount(r *http.Request) (state.Account, bool) {
	cookie, err := sessioncookie.Get(r)
	if err != nil {
		return state.Account{}, false
	}
	envelope, err := s.sessions.Verify(cookie.Value)
	if err != nil {
		return state.Account{}, false
	}
	account, err := s.store.AccountByID(r.Context(), envelope.AccountID)
	return account, err == nil && s.signedInBrowserAs(r, account)
}

func (s *server) invalidEmailVerification(w http.ResponseWriter, r *http.Request) {
	problem := api.ErrVerificationLinkInvalid()
	if !api.AcceptsHTML(r) {
		api.WriteProblemForRequest(w, r, problem)
		return
	}
	data := map[string]any{"Invalid": true, "SignInURL": verificationSignInURL(emailVerificationPath)}
	if account, ok := s.verificationBrowserAccount(r); ok && !account.EmailVerified() {
		data["ResendToken"] = s.verificationResendFormToken(w, account.ID)
	}
	s.renderEmailVerification(w, r, problem.Status, problem.Title, data)
}

// The web console honors reauth=1 even with a stored session hint. The
// backend sign-in page uses verify=1 for verification-specific instructions.
func verificationSignInURL(next string) string {
	return loginPath + "?reauth=1&verify=1&next=" + url.QueryEscape(next)
}

func (s *server) renderEmailVerification(w http.ResponseWriter, r *http.Request, status int, title string, data map[string]any) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	renderAuthPage(w, r, s.log, status, dashboard.Page{Title: title, Body: "email_verified", Data: data})
}
