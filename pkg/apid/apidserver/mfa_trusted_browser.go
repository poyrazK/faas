package apidserver

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindinghash"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/onebox-faas/faas/pkg/state"
)

// Remember this browser (ADR 20261108150000). A browser that passes the
// authenticator check at sign-in may ask to be remembered: it gets a random
// host-only cookie and the account keeps the token's keyed digest, the
// browser's user-agent family and an expiry. A later sign-in from that
// browser, same family, starts a full session instead of an MFA-pending one.
// The cookie skips only the sign-in challenge: step-up for sensitive
// actions still asks for a code, operators never skip it, and a browser
// remembered before the current enrollment is not trusted. Changing the
// password or email and signing out everywhere forget every browser.

const (
	mfaTrustCookie     = "faas_mfa_trust"
	mfaTrustTokenBytes = 32

	dashboardMFATrustForgetAction    = "account_mfa_trust_forget"
	dashboardMFATrustForgetCSRF      = "faas_csrf_account_mfa_trust_forget"
	dashboardMFATrustForgetAllAction = "account_mfa_trust_forget_all"
	dashboardMFATrustForgetAllCSRF   = "faas_csrf_account_mfa_trust_forget_all"
)

func (s *server) trustedBrowserStore() (state.MFATrustedBrowserStore, bool) {
	st, ok := s.store.(state.MFATrustedBrowserStore)
	return st, ok
}

// sessionMFAChallengePending reports whether r's session is waiting for the
// sign-in authenticator check (not a step-up of a full session).
func (s *server) sessionMFAChallengePending(r *http.Request, acct state.Account) bool {
	c, err := sessioncookie.Get(r)
	if err != nil || c.Value == "" {
		return false
	}
	env, err := s.sessions.Verify(c.Value)
	return err == nil && env.AccountID == acct.ID && session.IsMFAPending(env) && s.sessionMFAPending(acct)
}

// canRememberBrowser reports whether acct may skip the sign-in check from
// a remembered browser.
func (s *server) canRememberBrowser(acct state.Account) bool {
	if !acct.MFAEnrolled() || s.isOperator(acct) {
		return false
	}
	_, ok := s.trustedBrowserStore()
	return ok
}

// rememberBrowser stores a trust token for r's browser and sets its cookie.
// Failure only logs: the sign-in itself succeeded.
func (s *server) rememberBrowser(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, ok := s.trustedBrowserStore()
	if !ok || !s.canRememberBrowser(acct) {
		return
	}
	raw := make([]byte, mfaTrustTokenBytes)
	defer clear(raw)
	if _, err := rand.Read(raw); err != nil {
		s.log.Error("mfa_trust.token", "err", err, "account_id", acct.ID)
		return
	}
	ip := clientIPFromRequest(r)
	family := bindinghash.UAFamily(r.UserAgent())
	expiresAt := time.Now().Add(api.MFATrustedBrowserTTL)
	if err := st.IssueMFATrustedBrowser(r.Context(), api.HashToken(raw), acct.ID, securityActivityClean(family, 64), securityActivityClean(ip, 64), expiresAt, api.MFATrustedBrowsersMax); err != nil {
		s.log.Error("mfa_trust.issue", "err", err, "account_id", acct.ID)
		return
	}
	secure := requestIsHTTPS(r)
	http.SetCookie(w, &http.Cookie{
		Name:     oauthFlowCookieName(r, mfaTrustCookie),
		Value:    base64.RawURLEncoding.EncodeToString(raw),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(api.MFATrustedBrowserTTL.Seconds()),
	})
	s.audit.Emit(r.Context(), "account.mfa_browser_remembered", &acct.ID, map[string]any{"ip": ip, "ua_family": family})
}

// trustedBrowserSkipsMFA reports whether r comes from a browser accountID
// remembered, so the new session needs no authenticator check.
func (s *server) trustedBrowserSkipsMFA(ctx context.Context, r *http.Request, accountID string) bool {
	st, ok := s.trustedBrowserStore()
	if !ok {
		return false
	}
	c, err := r.Cookie(oauthFlowCookieName(r, mfaTrustCookie))
	if err != nil || c.Value == "" {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil || len(raw) != mfaTrustTokenBytes {
		return false
	}
	defer clear(raw)
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil || !s.canRememberBrowser(acct) {
		return false
	}
	family := bindinghash.UAFamily(r.UserAgent())
	used, err := st.UseMFATrustedBrowser(ctx, api.HashToken(raw), accountID, securityActivityClean(family, 64))
	if err != nil {
		s.log.Warn("mfa_trust.use", "err", err, "account_id", accountID)
		return false
	}
	if used && s.audit != nil {
		s.audit.Emit(ctx, "auth.mfa_trusted_browser_used", &accountID, map[string]any{"ip": clientIPFromRequest(r), "ua_family": family})
	}
	return used
}

// forgetTrustedBrowsers forgets every remembered browser of accountID.
func (s *server) forgetTrustedBrowsers(ctx context.Context, accountID, reason string) {
	st, ok := s.trustedBrowserStore()
	if !ok {
		return
	}
	n, err := st.RevokeAllMFATrustedBrowsers(ctx, accountID)
	if err != nil {
		s.log.Warn("mfa_trust.revoke_all", "err", err, "account_id", accountID)
		return
	}
	if n > 0 && s.audit != nil {
		s.audit.Emit(ctx, "account.mfa_browsers_forgotten", &accountID, map[string]any{"count": n, "reason": reason})
	}
}
