package apidserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// Account page profile and security forms (ADR 20261107103000): resend
// verification, sign-in email, display name, browser sessions and linked
// providers. A refused form returns to the account page with a fixed
// message chosen by the problem code; nothing from the request is echoed.

const (
	dashboardDisplayNameAction    = "account_display_name"
	dashboardDisplayNameCookie    = "faas_csrf_account_display_name"
	dashboardSessionRevokeAction  = "account_session_revoke"
	dashboardSessionRevokeCookie  = "faas_csrf_account_session_revoke"
	dashboardProviderUnlinkAction = "account_provider_unlink"
	dashboardProviderUnlinkCookie = "faas_csrf_account_provider_unlink"
	accountPagePath               = "/dashboard/account"
	accountPageActivityRows       = 20
	accountStepUpTTL              = 5 * time.Minute
)

// accountFormErrors are the account page banners for a refused form.
var accountFormErrors = map[string]string{
	"expired":      "That form expired. Reload the page and try again.",
	"step_up":      "Confirm with your authenticator first: open Manage two-factor authentication, verify, then try again within 5 minutes.",
	"mfa":          "Finish setting up two-factor authentication first.",
	"password":     "Your current password was not correct.",
	"invalid":      "Check the value you entered and try again.",
	"last_method":  "That is your last way to sign in. Set a password or link another provider first.",
	"failed":       "Gregale could not save that change. Try again in a moment.",
	"link_session": "Linking needs this browser to stay signed in. Sign in again and press Link once more.",
	"link_taken":   "That provider account already signs in to another Gregale account. Remove it there first.",
}

// fillAccountProfileForms mints the form tokens and lists the account page
// renders.
func (s *server) fillAccountProfileForms(w http.ResponseWriter, r *http.Request, acct state.Account, data *dashboard.AccountData) error {
	type form struct {
		dst            *string
		action, cookie string
	}
	forms := []form{
		{&data.EmailChangeConfirmToken, dashboardEmailChangeAct, dashboardEmailChangeCSRFC},
		{&data.DisplayNameConfirmToken, dashboardDisplayNameAction, dashboardDisplayNameCookie},
		{&data.SessionRevokeConfirmToken, dashboardSessionRevokeAction, dashboardSessionRevokeCookie},
		{&data.ProviderUnlinkConfirmToken, dashboardProviderUnlinkAction, dashboardProviderUnlinkCookie},
		{&data.ProviderLinkConfirmToken, dashboardProviderLinkAction, dashboardProviderLinkCookie},
		{&data.TrustedBrowserForgetToken, dashboardMFATrustForgetAction, dashboardMFATrustForgetCookie},
	}
	if !acct.EmailVerified() {
		forms = append(forms, form{&data.VerifyResendConfirmToken, dashboardVerifyResendAction, dashboardVerifyResendCSRFCookie})
	}
	for _, f := range forms {
		tok, err := s.issueDashboardFormToken(w, f.action, f.cookie, acct.ID)
		if err != nil {
			return err
		}
		*f.dst = tok
	}
	if methods, err := s.signInMethods(r.Context(), acct); err == nil {
		data.HasPassword = methods.Password
		linked := map[string]bool{}
		for _, p := range methods.Providers {
			linked[p.Provider] = true
			data.Providers = append(data.Providers, dashboard.AccountProviderRow{Provider: p.Provider, Email: p.Email, LinkedAt: p.LinkedAt})
		}
		for _, provider := range []string{"google", "github"} {
			if _, _, enabled := s.oauthLinkStart(provider); enabled && !linked[provider] {
				data.LinkProviders = append(data.LinkProviders, dashboard.AccountLinkOption{
					Provider: provider,
					Name:     providerDisplayName(provider),
				})
			}
		}
	}
	data.MFAEnabled = acct.MFAEnrolled()
	data.EmailChangeNeedsPassword = data.HasPassword && !data.MFAEnabled
	if flow, ok := s.store.(state.AccountFlowStore); ok {
		data.DisplayName, _ = flow.AccountDisplayName(r.Context(), acct.ID)
	}
	s.fillAccountSessions(r, acct, data)
	s.fillTrustedBrowsers(r, acct, data)
	if events, err := s.securityActivity(r.Context(), acct.ID, time.Now()); err == nil {
		for i, e := range events {
			if i == accountPageActivityRows {
				data.MoreActivity = true
				break
			}
			data.Activity = append(data.Activity, dashboard.AccountActivityRow{
				At: strings.Replace(strings.TrimSuffix(e.At, "Z"), "T", " ", 1) + " UTC", Summary: e.Summary, IP: e.IP, Detail: e.Detail,
			})
		}
	}
	return nil
}

func (s *server) fillAccountSessions(r *http.Request, acct state.Account, data *dashboard.AccountData) {
	sessions, err := s.store.ListSessions(r.Context(), acct.ID)
	if err != nil {
		return
	}
	current, _ := sessionFrom(r)
	for _, sess := range sessions {
		data.Sessions = append(data.Sessions, dashboard.AccountSessionRow{
			ID: sess.ID, IssuedAt: sess.IssuedAt.UTC().Format("2006-01-02 15:04 UTC"),
			IssuedIP: sess.IssuedIP, IssuedUA: sess.IssuedUA,
			LastSeen: formatLastSeen(sess.LastSeenAt), IsCurrent: sess.ID == current.ID,
		})
	}
}

// accountFlowFlash is the account page banner after one of these forms.
func accountFlowFlash(r *http.Request) string {
	q := r.URL.Query()
	if msg, ok := accountFormErrors[q.Get("account_error")]; ok {
		return msg
	}
	switch {
	case q.Get("verification") == "sent":
		return "If your address is still unverified, a new 24-hour verification link is on its way."
	case q.Get("email_change") == "sent":
		return "Check the new address: open the link we sent within 1 hour to finish the change."
	case q.Get("display_name") == "saved":
		return "Display name saved."
	case q.Get("session") == "revoked":
		return "That browser was signed out."
	case q.Get("provider") == "removed":
		return "Sign-in method removed."
	case q.Get("password_reset") == "keys":
		return "Password changed and every browser signed out. API keys you created still work: review them below and delete any you do not recognise."
	case q.Get("trusted_browser") == "forgotten":
		return "Forgotten. That browser asks for a code at its next sign-in."
	case q.Get("provider") == "linked":
		return "Sign-in method linked. A notice was sent to your email address."
	}
	return ""
}

// accountFormError picks the banner key for a refused form's problem.
func accountFormError(status int, code string) string {
	switch {
	case code == api.CodeStepUpRequired:
		return "step_up"
	case code == api.CodeMFARequired:
		return "mfa"
	case status == http.StatusUnauthorized:
		return "password"
	case status == http.StatusConflict && code == api.CodeValidation:
		return "last_method"
	case status == http.StatusBadRequest || code == api.CodeValidation:
		return "invalid"
	}
	return "failed"
}

func redirectAccountError(w http.ResponseWriter, r *http.Request, key string) {
	http.Redirect(w, r, accountPagePath+"?account_error="+url.QueryEscape(key), http.StatusSeeOther)
}

// runAccountForm runs a handler that writes JSON problems, and turns its
// outcome into a redirect back to the account page.
func runAccountForm(w http.ResponseWriter, r *http.Request, okQuery string, run func(http.ResponseWriter) bool) {
	captured := &capturedResponse{header: http.Header{}}
	if run(captured) {
		http.Redirect(w, r, accountPagePath+"?"+okQuery, http.StatusSeeOther)
		return
	}
	var p struct {
		Status int    `json:"status"`
		Code   string `json:"code"`
	}
	_ = json.Unmarshal(captured.body.Bytes(), &p)
	if p.Status == 0 {
		p.Status = captured.status
	}
	redirectAccountError(w, r, accountFormError(p.Status, p.Code))
}

// accountFormAccount is the signed-in account of a CSRF-checked account
// page form; it answers the request itself when ok is false.
func (s *server) accountFormAccount(w http.ResponseWriter, r *http.Request, action, cookie string) (state.Account, bool) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return state.Account{}, false
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, action, acct.ID, cookie); err != nil {
		redirectAccountError(w, r, "expired")
		return state.Account{}, false
	}
	return acct, true
}

// dashboardSetDisplayName is POST /dashboard/account/display-name.
func (s *server) dashboardSetDisplayName(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountFormAccount(w, r, dashboardDisplayNameAction, dashboardDisplayNameCookie)
	if !ok {
		return
	}
	if problem := s.setDisplayName(r, acct, r.FormValue("display_name")); problem != nil {
		redirectAccountError(w, r, accountFormError(problem.Status, problem.Code))
		return
	}
	http.Redirect(w, r, accountPagePath+"?display_name=saved", http.StatusSeeOther)
}

// dashboardRevokeSession is POST /dashboard/account/sessions/{id}/revoke.
// Signing out the current browser lands on the sign-in page.
func (s *server) dashboardRevokeSession(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountFormAccount(w, r, dashboardSessionRevokeAction, dashboardSessionRevokeCookie)
	if !ok {
		return
	}
	id := r.PathValue("id")
	revoked, err := s.store.RevokeSession(r.Context(), id, acct.ID)
	if err != nil || !revoked {
		redirectAccountError(w, r, "failed")
		return
	}
	s.audit.Emit(r.Context(), "auth.session.revoke", &acct.ID, map[string]any{"sid": id, "reason": "explicit"})
	if current, ok := sessionFrom(r); ok && current.ID == id {
		s.clearSessionCookie(w, r)
		http.Redirect(w, r, loginPath, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, accountPagePath+"?session=revoked", http.StatusSeeOther)
}

// dashboardUnlinkProvider is POST /dashboard/account/providers/{provider}/unlink.
// An account with two-factor authentication confirms with a fresh code.
func (s *server) dashboardUnlinkProvider(w http.ResponseWriter, r *http.Request) {
	acct, ok := s.accountFormAccount(w, r, dashboardProviderUnlinkAction, dashboardProviderUnlinkCookie)
	if !ok {
		return
	}
	if acct.MFAEnrolled() {
		ts, has := authmw.StepUpFrom(r)
		if !has || ts.IsZero() || time.Since(ts) > accountStepUpTTL {
			redirectAccountError(w, r, "step_up")
			return
		}
	}
	if problem := s.unlinkProvider(r.Context(), acct, strings.ToLower(r.PathValue("provider"))); problem != nil {
		redirectAccountError(w, r, accountFormError(problem.Status, problem.Code))
		return
	}
	http.Redirect(w, r, accountPagePath+"?provider=removed", http.StatusSeeOther)
}

// patchAccountProfile is PATCH /v1/account.
func (s *server) patchAccountProfile(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var in struct {
		DisplayName *string `json:"display_name"`
	}
	if err := decodeJSON(r, &in); err != nil || in.DisplayName == nil {
		api.WriteProblem(w, api.ErrValidation(`body must be {"display_name": "..."}`))
		return
	}
	if problem := s.setDisplayName(r, acct, *in.DisplayName); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"display_name": strings.TrimSpace(*in.DisplayName)})
}

func (s *server) setDisplayName(r *http.Request, acct state.Account, raw string) *api.Problem {
	name, err := api.ValidateDisplayName(raw)
	if err != nil {
		return api.ErrValidation(err.Error())
	}
	flow, ok := s.store.(state.AccountFlowStore)
	if !ok {
		return api.ErrCapacity("display names are unavailable")
	}
	if err := flow.SetAccountDisplayName(r.Context(), acct.ID, name); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Account not found", "the account no longer exists")
		}
		s.log.Error("display_name.set", "err", err, "account_id", acct.ID)
		return api.ErrCapacity("could not save the display name; try again")
	}
	s.audit.Emit(r.Context(), "account.display_name_set", &acct.ID, nil)
	return nil
}
