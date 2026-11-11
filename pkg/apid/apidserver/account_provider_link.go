package apidserver

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// Linking a provider from a signed-in browser (ADR-20261108150000). The
// account page posts a CSRF-checked form; the handler drops a five-minute,
// host-only intent cookie naming the account and starts the provider's
// OAuth flow. When the provider calls back, the intent links whatever
// identity it returned to that account, whatever its email, provided the
// browser is still signed in to it. Starting a link needs a verified email
// and fresh presence (ADR 20261111090000). Without the intent the callback keeps
// its sign-in behaviour (match by subject, then by email).

const (
	oauthLinkCookie             = "faas_oauth_link"
	dashboardProviderLinkAction = "account_provider_link"
	dashboardProviderLinkCookie = "faas_csrf_account_provider_link"
)

// oauthLinkStart returns the provider's OAuth start and callback paths when
// it is enabled on this installation.
func (s *server) oauthLinkStart(provider string) (start, callback string, ok bool) {
	switch provider {
	case "google":
		return "/v1/auth/google", googleCallbackPath, s.oauthConfig.Google.Enabled()
	case "github":
		return "/v1/auth/github", githubCallbackPath, s.oauthConfig.GitHub.Enabled()
	}
	return "", "", false
}

// dashboardLinkProvider is POST /dashboard/account/providers/{provider}/link.
func (s *server) dashboardLinkProvider(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountFormAccount(w, r, dashboardProviderLinkAction, dashboardProviderLinkCookie)
	if !ok {
		return
	}
	// A linked provider signs in by its own identity, so linking one is a
	// sign-in method change: the address must be proven (an unverified
	// account may be a squatter's) and the browser present (account-flow
	// audit 2026-11-10, ADR 20261111090000).
	if !acct.EmailVerified() {
		s.audit.Emit(r.Context(), "account.sign_in_method_link_refused", &acct.ID, map[string]any{"reason": "email_unverified"})
		redirectAccountError(w, r, "verify_email")
		return
	}
	if !s.accountFormPresence(w, r, acct, "sign_in_method_link") {
		return
	}
	provider := strings.ToLower(r.PathValue("provider"))
	start, callback, enabled := s.oauthLinkStart(provider)
	if !enabled {
		redirectAccountError(w, r, "invalid")
		return
	}
	setOAuthFlowCookie(w, r, oauthLinkCookie, acct.ID, callback)
	http.Redirect(w, r, start+"?next="+url.QueryEscape(accountPagePath), http.StatusSeeOther)
}

// finishOAuthLink completes a link the account page started. It reports
// false when the callback carries no link intent, so the caller signs in as
// usual; otherwise it has answered the request.
func (s *server) finishOAuthLink(w http.ResponseWriter, r *http.Request, provider, sub, email string, verified bool) bool {
	c, err := oauthFlowCookie(r, oauthLinkCookie)
	if err != nil || c.Value == "" {
		return false
	}
	_, callback, _ := s.oauthLinkStart(provider)
	clearOAuthFlowCookie(w, r, oauthLinkCookie, callback)
	clearOAuthNext(w, r, callback)
	ctx := r.Context()
	acct, err := s.store.AccountByID(ctx, c.Value)
	if err != nil || !s.signedInBrowserAs(r, acct) {
		redirectAccountError(w, r, "link_session")
		return true
	}
	if link, err := s.store.OAuthLinkByProviderSubject(ctx, provider, sub); err == nil && link.AccountID != acct.ID {
		s.audit.Emit(ctx, "account.sign_in_method_link_refused", &acct.ID, map[string]any{"provider": provider, "reason": "linked_elsewhere"})
		redirectAccountError(w, r, "link_taken")
		return true
	}
	if flow, ok := s.store.(state.AccountFlowStore); ok {
		if err := flow.ClearOAuthUnlink(ctx, acct.ID, provider); err != nil {
			s.log.Error("oauth_link.clear_unlink", "provider", provider, "err", err)
			redirectAccountError(w, r, "failed")
			return true
		}
	}
	if err := s.store.UpsertOAuthLink(ctx, acct.ID, provider, sub, strings.ToLower(strings.TrimSpace(email)), verified); err != nil {
		key := "failed"
		if errors.Is(err, state.ErrConflict) {
			key = "link_taken"
		}
		s.log.Warn("oauth_link.upsert", "provider", provider, "err", err)
		redirectAccountError(w, r, key)
		return true
	}
	s.alertSignInMethodLinked(ctx, acct, provider)
	http.Redirect(w, r, accountPagePath+"?provider=linked", http.StatusSeeOther)
	return true
}

func clearOAuthFlowCookie(w http.ResponseWriter, r *http.Request, base, callbackPath string) {
	secure := requestIsHTTPS(r)
	path := callbackPath
	if secure {
		path = "/"
	}
	http.SetCookie(w, &http.Cookie{Name: oauthFlowCookieName(r, base), Value: "", Path: path, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
