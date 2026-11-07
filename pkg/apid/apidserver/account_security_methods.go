package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authcode"
	"github.com/onebox-faas/faas/pkg/bindinghash"
	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/sessioncookie"
	"github.com/onebox-faas/faas/pkg/state"
)

// Recovery codes, linked sign-in methods and sign-in notices
// (ADR 20261107103000).

const (
	csrfActionMFARecoveryCodes = "mfa_recovery_codes"
	csrfActionSignInUnlink     = "sign_in_method_unlink"
)

// mfaRegenerateRecoveryCodes is POST /v1/account/mfa/recovery-codes. A
// browser session with a fresh step-up replaces every recovery code. The
// route uses the strict step-up, so an API key is refused: a leaked key
// must not be able to mint codes that stand in for the authenticator.
func (s *server) mfaRegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := middleware.VerifyAuthenticated(s.sessions, r, csrfActionMFARecoveryCodes, acct.ID); err != nil {
		api.WriteProblem(w, csrfMismatchProblem)
		return
	}
	if !acct.MFAEnrolled() {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Two-factor authentication is off", "set up an authenticator before creating recovery codes"))
		return
	}
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("recovery codes are unavailable"))
		return
	}
	plaintexts, hashes, err := authcode.NewRecoveryCodes(authcode.RecoveryCodeCount)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate recovery codes"))
		return
	}
	if err := flow.ReplaceRecoveryCodes(r.Context(), acct.ID, hashes); err != nil {
		s.log.Error("mfa.recovery_codes.replace", "err", err, "account_id", acct.ID)
		api.WriteProblem(w, api.ErrCapacity("could not save recovery codes"))
		return
	}
	s.audit.Emit(r.Context(), "auth.mfa.recovery_codes_regenerated", &acct.ID, nil)
	subject, body := mailpkg.RecoveryCodesRegeneratedBody(acct.Email, time.Now())
	s.sendSecurityNotice(r.Context(), acct, "recovery_codes_regenerated", subject, body)
	writeJSON(w, http.StatusOK, api.RecoveryCodesResponse{RecoveryCodes: plaintexts})
}

// listSignInMethods is GET /v1/account/sign-in-methods.
func (s *server) listSignInMethods(w http.ResponseWriter, r *http.Request, acct state.Account) {
	resp, err := s.signInMethods(r.Context(), acct)
	if err != nil {
		s.log.Error("sign_in_methods.list", "err", err, "account_id", acct.ID)
		api.WriteProblem(w, api.ErrCapacity("could not load sign-in methods"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) signInMethods(ctx context.Context, acct state.Account) (api.SignInMethodsResponse, error) {
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		return api.SignInMethodsResponse{}, errors.New("account flow store unavailable")
	}
	links, err := flow.ListOAuthLinks(ctx, acct.ID)
	if err != nil {
		return api.SignInMethodsResponse{}, err
	}
	_, perr := s.store.AccountPasswordByAccountID(ctx, acct.ID)
	if perr != nil && !errors.Is(perr, state.ErrNotFound) {
		return api.SignInMethodsResponse{}, perr
	}
	resp := api.SignInMethodsResponse{Password: perr == nil, MFA: acct.MFAEnrolled(), Providers: []api.LinkedSignIn{}}
	for _, l := range links {
		resp.Providers = append(resp.Providers, api.LinkedSignIn{Provider: l.Provider, Email: l.Email, LinkedAt: l.CreatedAt.UTC().Format(time.RFC3339)})
	}
	return resp, nil
}

// unlinkSignInMethod is DELETE /v1/account/sign-in-methods/{provider}. It
// refuses to remove the last way to sign in besides email links.
func (s *server) unlinkSignInMethod(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if err := s.verifySessionCSRF(r, csrfActionSignInUnlink, acct.ID); err != nil {
		api.WriteProblem(w, csrfMismatchProblem)
		return
	}
	if problem := s.unlinkProvider(r.Context(), acct, r.PathValue("provider")); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// unlinkProvider removes a linked provider unless it is the last way to
// sign in. Shared by the API and the account page.
func (s *server) unlinkProvider(ctx context.Context, acct state.Account, provider string) *api.Problem {
	methods, err := s.signInMethods(ctx, acct)
	if err != nil {
		return api.ErrCapacity("could not load sign-in methods")
	}
	linked, others := false, 0
	for _, p := range methods.Providers {
		if p.Provider == provider {
			linked = true
		} else {
			others++
		}
	}
	if !linked {
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not linked", provider+" is not linked to this account")
	}
	if !methods.Password && others == 0 {
		return api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Last sign-in method", "set a password or link another provider before removing this one")
	}
	if _, err := s.store.(state.AccountFlowStore).DeleteOAuthLink(ctx, acct.ID, provider); err != nil {
		return api.ErrCapacity("could not remove the sign-in method")
	}
	s.audit.Emit(ctx, "account.sign_in_method_unlinked", &acct.ID, map[string]any{"provider": provider})
	return nil
}

// errOAuthProviderUnlinked refuses an OAuth sign-in whose provider the
// account removed; only a browser signed in to the account can link it again.
var errOAuthProviderUnlinked = errors.New("oauth provider was unlinked from this account")

// linkOAuthByEmail binds a provider identity to the existing account with
// the same email. A provider the owner removed stays removed: signing in
// with it is refused unless this browser is already signed in to the
// account (the account page's "Link" button), which links it again.
func (s *server) linkOAuthByEmail(r *http.Request, acct state.Account, provider, sub, email string, verified bool) error {
	ctx := r.Context()
	flow, ok := s.store.(state.AccountFlowStore)
	if ok {
		unlinked, err := flow.OAuthProviderUnlinked(ctx, acct.ID, provider)
		if err != nil {
			return fmt.Errorf("check unlinked %s: %w", provider, err)
		}
		if unlinked {
			if !s.signedInBrowserAs(r, acct) {
				s.audit.Emit(ctx, "auth.oauth_unlinked_refused", &acct.ID, map[string]any{"provider": provider})
				return errOAuthProviderUnlinked
			}
			if err := flow.ClearOAuthUnlink(ctx, acct.ID, provider); err != nil {
				return fmt.Errorf("clear unlinked %s: %w", provider, err)
			}
		}
	}
	if err := s.store.UpsertOAuthLink(ctx, acct.ID, provider, sub, email, verified); err != nil {
		s.log.Error("oauth.upsert_link", "provider", provider, "err", err)
		return nil
	}
	s.alertSignInMethodLinked(ctx, acct, provider)
	return nil
}

// signedInBrowserAs reports whether r carries a live, fully signed-in
// dashboard session for acct.
func (s *server) signedInBrowserAs(r *http.Request, acct state.Account) bool {
	c, err := sessioncookie.Get(r)
	if err != nil || c.Value == "" {
		return false
	}
	env, err := s.sessions.Verify(c.Value)
	if err != nil || env.AccountID != acct.ID {
		return false
	}
	if session.IsMFAPending(env) && s.sessionMFAPending(acct) {
		return false
	}
	_, ok := s.dashboardSessionRow(&headerCaptureWriter{header: http.Header{}}, r, env)
	return ok
}

// writeOAuthProviderUnlinked answers a sign-in refused by linkOAuthByEmail.
func writeOAuthProviderUnlinked(w http.ResponseWriter, r *http.Request, provider string) {
	name := providerDisplayName(provider)
	api.WriteProblemForRequest(w, r, api.NewProblem(http.StatusForbidden, api.CodeForbidden,
		name+" sign-in was removed",
		"This account no longer signs in with "+name+". Sign in with your password or an email link, then link "+name+" again from your account page."))
}

func providerDisplayName(provider string) string {
	switch provider {
	case "google":
		return "Google"
	case "github":
		return "GitHub"
	}
	return provider
}

// alertSignInMethodLinked mails the account when an OAuth provider is newly
// linked to an existing account by matching email.
func (s *server) alertSignInMethodLinked(ctx context.Context, acct state.Account, provider string) {
	s.audit.Emit(ctx, "account.sign_in_method_linked", &acct.ID, map[string]any{"provider": provider})
	subject, body := mailpkg.SignInMethodLinkedBody(acct.Email, provider, time.Now())
	s.sendSecurityNotice(ctx, acct, "sign_in_method_linked", subject, body)
}

// signInIsNew reports whether a browser sign-in comes from a device and
// network no session of the last NewSignInLookback used. An account with
// no recent session (its first sign-in) is not "new": there is nothing to
// compare against and the owner just signed up.
func (s *server) signInIsNew(ctx context.Context, accountID, ip, ua string) bool {
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		return false
	}
	recent, err := flow.RecentSignIns(ctx, accountID, time.Now().Add(-api.NewSignInLookback))
	if err != nil || len(recent) == 0 {
		return false
	}
	family := bindinghash.UAFamily(ua)
	for _, rec := range recent {
		if sameNetwork(rec.IssuedIP, ip) && bindinghash.UAFamily(rec.IssuedUA) == family {
			return false
		}
	}
	return true
}

func (s *server) sendNewSignInNotice(ctx context.Context, accountID, ip, ua string) {
	acct, err := s.store.AccountByID(ctx, accountID)
	if err != nil {
		return
	}
	s.audit.Emit(ctx, "auth.new_sign_in_notice", &accountID, map[string]any{"issued_ip": ip})
	subject, body := mailpkg.NewSignInBody(acct.Email, ip, ua, time.Now())
	s.sendSecurityNotice(ctx, acct, "new_sign_in", subject, body)
}

// sendSecurityNotice mails an informational notice, bounded by the
// per-account security-mail throttle. Failures stay server-side.
func (s *server) sendSecurityNotice(ctx context.Context, acct state.Account, kind, subject, body string) {
	if !s.allowSecurityMail(ctx, acct, kind) {
		return
	}
	s.sendNoticeInBackground(ctx, kind, acct.ID, Message{To: []string{acct.Email}, Subject: subject, TextBody: body})
}

// sendNoticeInBackground mails msg off the request path, so a slow mail
// server never delays sign-in or the change that caused the notice.
func (s *server) sendNoticeInBackground(ctx context.Context, kind, accountID string, msg Message) {
	ctx = context.WithoutCancel(ctx)
	if !s.resetMail.try(func() {
		ctx, cancel := context.WithTimeout(ctx, api.PasswordResetMailTimeout)
		defer cancel()
		if err := s.mailer.Send(ctx, msg); err != nil {
			s.log.Error("security_notice.mailer", "kind", kind, "err", err, "account_id", accountID)
		}
	}) {
		s.log.Warn("security_notice.busy", "kind", kind, "account_id", accountID)
	}
}

// verifySessionCSRF checks the double-submit token on browser-session
// requests. A bearer key is never sent by a browser on its own, so CSRF
// does not apply to it; the CLI manages sessions with a key.
func (s *server) verifySessionCSRF(r *http.Request, action, accountID string) error {
	if _, ok := sessionFrom(r); !ok {
		return nil
	}
	return middleware.VerifyAuthenticated(s.sessions, r, action, accountID)
}

// revokeAllBrowserSessions signs out every dashboard session for a caller
// authenticated with an API key.
func (s *server) revokeAllBrowserSessions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	n, err := s.store.RevokeAllSessions(r.Context(), acct.ID, uuid.Nil.String())
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "revoke dashboard sessions",
			"Gregale could not sign out your browsers.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	s.audit.Emit(r.Context(), "auth.sessions.revoke_all", &acct.ID, map[string]any{"revoked_count": n, "via": "api_key"})
	writeJSON(w, http.StatusOK, api.SessionsRevokeAllResponse{Revoked: n})
}

// sameNetwork compares IPv4 addresses by /24 and IPv6 by /48, so a
// changing address from the same provider is not a "new" sign-in.
func sameNetwork(a, b string) bool {
	ipA, ipB := net.ParseIP(a), net.ParseIP(b)
	if ipA == nil || ipB == nil {
		return a == b
	}
	if v4a, v4b := ipA.To4(), ipB.To4(); v4a != nil || v4b != nil {
		return v4a != nil && v4b != nil && v4a.Mask(net.CIDRMask(24, 32)).Equal(v4b.Mask(net.CIDRMask(24, 32)))
	}
	return ipA.Mask(net.CIDRMask(48, 128)).Equal(ipB.Mask(net.CIDRMask(48, 128)))
}
