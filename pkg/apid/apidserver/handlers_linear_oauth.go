package apidserver

import (
	"context"
	"crypto/subtle"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const linearOAuthCookie = "faas_linear_oauth_state"

func (s *server) linearOAuthService() (issuetrackers.LinearOAuthService, bool) {
	store, ok := s.store.(state.LinearOAuthStore)
	provider := s.linearOAuthProvider
	if provider == nil {
		provider = issuetracker.NewClient(nil)
	}
	verifier := s.nativeLinearVerifier
	if verifier == nil {
		verifier = issuetracker.NewClient(nil)
	}
	// Linear OAuth credentials and consent flows are account values (ADR
	// 20261022111812). No recipient leaves the service unconfigured.
	recipients, _ := accountRecipients(hostSecretRecipient())
	return issuetrackers.LinearOAuthService{Store: store, Provider: provider, Verifier: verifier, Config: s.linearOAuthConfig, Recipients: recipients, Identities: accountIdentities()}, ok
}
func (s *server) dashboardLinearOAuthForm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.linearConnectionAccess(w, r, acct)
	if !ok {
		return
	}
	service, ok := s.linearOAuthService()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Linear OAuth storage unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	action := r.PostForm.Get("action")
	if action != "oauth_revoke" {
		if _, ok = s.issueStore(w, acct); !ok {
			return
		}
		if !acct.MayDeploy() {
			api.WriteProblem(w, api.ErrValidation("account cannot configure provider access"))
			return
		}
	}
	if action == "oauth_start" {
		s.startDashboardLinearOAuth(w, r, acct, app, service)
		return
	}
	if action == "oauth_finish" {
		s.finishDashboardLinearOAuth(w, r, acct, app, service)
		return
	}
	row, err := service.Store.GetIssueTrackerLinearConnection(ctx, app.ID, acct.ID)
	expected, e := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || e != nil || expected != row.Connection.Revision {
		api.WriteProblem(w, api.ErrValidation("reload the Linear connection before revoking"))
		return
	}
	if err = service.Revoke(ctx, row); err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	s.auditLinearOAuth(r, acct, app, "revoked")
	s.redirectLinearOAuth(w, r, app.Slug, r.PathValue("issue_id"))
}
func (s *server) startDashboardLinearOAuth(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, service issuetrackers.LinearOAuthService) {
	// Validate the issue route before retaining a return destination.
	if _, _, ok := s.issueTrackerLinksAccess(w, r, acct); !ok {
		return
	}
	expected, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || expected < 0 {
		api.WriteProblem(w, api.ErrValidation("reload the Linear connection"))
		return
	}
	nonce, target, err := service.Start(r.Context(), app.ID, acct.ID, r.PathValue("issue_id"), expected)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: linearOAuthCookie, Value: nonce, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(api.IssueTrackerLinearOAuthFlowTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (s *server) finishDashboardLinearOAuth(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, service issuetrackers.LinearOAuthService) {
	nonce, ok := linearOAuthNonce(r, r.PostForm.Get("state"))
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Linear authorization state did not match; restart consent"))
		return
	}
	if _, err := service.Finish(r.Context(), nonce, app.ID, acct.ID, r.PathValue("issue_id"), r.PostForm.Get("team_id"), r.PostForm.Get("webhook_secret")); err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: linearOAuthCookie, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	s.auditLinearOAuth(r, acct, app, "connected")
	s.redirectLinearOAuth(w, r, app.Slug, r.PathValue("issue_id"))
}
func linearOAuthNonce(r *http.Request, value string) (string, bool) {
	cookie, err := r.Cookie(linearOAuthCookie)
	return value, err == nil && len(value) == 64 && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(value)) == 1
}
func (s *server) linearOAuthCallback(w http.ResponseWriter, r *http.Request) {
	_, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.linearOAuthCallbackForAccount)))(w, r)
}
func (s *server) linearOAuthCallbackForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	nonce, ok := linearOAuthNonce(r, r.URL.Query().Get("state"))
	if !ok {
		api.WriteProblem(w, api.ErrValidation("Linear authorization state did not match; restart consent"))
		return
	}
	service, ok := s.linearOAuthService()
	if !ok || !service.Config.Enabled() {
		api.WriteProblem(w, api.ErrCapacity("Linear OAuth unavailable"))
		return
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return
	}
	if !acct.MayDeploy() {
		api.WriteProblem(w, api.ErrValidation("account cannot configure Linear access"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.Query().Get("error") != "" {
		_ = service.Store.DeleteLinearOAuthFlow(ctx, issuetrackers.LinearOAuthStateHash(nonce), acct.ID)
		api.WriteProblem(w, api.ErrValidation("Linear authorization was declined; restart consent when ready"))
		return
	}
	if code := r.URL.Query().Get("code"); code != "" {
		if err := service.Exchange(ctx, nonce, acct.ID, code); err != nil {
			writeNativeIssueTrackerError(w, err)
			return
		}
		http.Redirect(w, r, "/oauth/linear/callback?state="+url.QueryEscape(nonce), http.StatusSeeOther)
		return
	}
	s.renderLinearOAuthPicker(w, r, acct, service, nonce)
}
func (s *server) renderLinearOAuthPicker(w http.ResponseWriter, r *http.Request, acct state.Account, service issuetrackers.LinearOAuthService, nonce string) {
	row, secret, err := service.Pending(r.Context(), nonce, acct.ID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	app, err := s.store.AppByID(r.Context(), row.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrValidation("Linear app ownership changed; restart consent"))
		return
	}
	csrf, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: csrf, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	view := dashboard.LinearOAuthPicker{AppSlug: app.Slug, IssueID: row.IssueID, State: nonce, CSRF: csrf, WorkspaceID: secret.Workspace.WorkspaceID, Teams: secret.Workspace.Teams}
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Select Linear team", Body: "issue_linear_oauth", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
func (s *server) redirectLinearOAuth(w http.ResponseWriter, r *http.Request, slug, issue string) {
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(slug)+"/issues?issue="+url.QueryEscape(issue), http.StatusSeeOther)
}
func (s *server) auditLinearOAuth(r *http.Request, acct state.Account, app state.App, result string) {
	if s.audit != nil {
		s.audit.Emit(r.Context(), "issue.tracker_linear_oauth_"+result, &acct.ID, map[string]any{"app_id": app.ID})
	}
}
