package apidserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const slackOAuthCookie = "faas_issue_slack_oauth"

func (s *server) slackOAuthService() (issueslack.OAuthService, bool) {
	st, ok := s.store.(state.IssueSlackOAuthStore)
	client := s.issueSlackOAuthClient
	if client == nil {
		client = issueslack.NewClient(nil)
	}
	service := issueslack.OAuthService{Store: st, Client: client, Config: s.issueSlackOAuthConfig}
	if setSecretRecipient != nil {
		service.Recipient = setSecretRecipient()
	}
	if mfaIdentities != nil {
		service.Identities = mfaIdentities()
	}
	return service, ok
}
func slackOAuthNonce(r *http.Request, value string) bool {
	cookie, err := r.Cookie(slackOAuthCookie)
	return err == nil && issueslack.ValidOAuthNonce(value) && subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(value)) == 1
}
func setSlackOAuthCookie(w http.ResponseWriter, nonce string) {
	maxAge := int(api.IssueSlackOAuthFlowTTL.Seconds())
	if nonce == "" {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: slackOAuthCookie, Value: nonce, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}
func (s *server) dashboardSlackOAuthAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	service, ok := s.slackOAuthService()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Slack installation storage unavailable"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackOAuthTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	action := r.PostForm.Get("settings_kind")
	if action != "slack_oauth_disconnect" && !acct.MayDeploy() {
		api.WriteProblem(w, api.ErrValidation("account cannot configure Slack access"))
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil || revision < 0 {
		api.WriteProblem(w, api.ErrValidation("reload Slack settings"))
		return
	}
	if action == "slack_oauth_start" {
		s.startDashboardSlackOAuth(w, r, acct, app, service, revision)
		return
	}
	err = s.applyDashboardSlackOAuth(w, r, acct, app, service, revision)

	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "issue."+action, &acct.ID, map[string]any{"app_id": app.ID})
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=slack#slack-oauth-settings", http.StatusSeeOther)
}
func (s *server) startDashboardSlackOAuth(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, service issueslack.OAuthService, revision int64) {
	if !acct.MayDeploy() {
		api.WriteProblem(w, api.ErrValidation("account cannot configure Slack access"))
		return
	}
	nonce, target, err := service.Start(r.Context(), app.ID, acct.ID, revision)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	setSlackOAuthCookie(w, nonce)
	http.Redirect(w, r, target, http.StatusSeeOther)
}
func (s *server) slackOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if _, ok := AccountFrom(r.Context()); !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.slackOAuthCallbackForAccount)))(w, r)
}
func (s *server) slackOAuthCallbackForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	nonce := r.URL.Query().Get("state")
	if !slackOAuthNonce(r, nonce) {
		api.WriteProblem(w, api.ErrValidation("Slack authorization state did not match; restart consent"))
		return
	}
	service, ok := s.slackOAuthService()
	if !ok || !service.Config.Enabled() {
		api.WriteProblem(w, api.ErrCapacity("Slack OAuth unavailable"))
		return
	}
	if _, ok = s.issueStore(w, acct); !ok {
		return
	}
	if !acct.MayDeploy() {
		api.WriteProblem(w, api.ErrValidation("account cannot configure Slack access"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackOAuthTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if r.URL.Query().Get("error") != "" {
		_, _ = service.Store.ClaimIssueSlackOAuth(ctx, issueslack.OAuthStateHash(nonce), acct.ID)
		setSlackOAuthCookie(w, "")
		api.WriteProblem(w, api.ErrValidation("Slack authorization declined; restart consent when ready"))
		return
	}
	if code := r.URL.Query().Get("code"); code != "" {
		if err := service.Exchange(ctx, nonce, acct.ID, code); err != nil {
			writeNativeIssueTrackerError(w, err)
			return
		}
		http.Redirect(w, r, "/oauth/slack/callback?state="+url.QueryEscape(nonce), http.StatusSeeOther)
		return
	}
	s.renderSlackOAuthConfirmation(w, r, acct, service, nonce)
}
func (s *server) renderSlackOAuthConfirmation(w http.ResponseWriter, r *http.Request, acct state.Account, service issueslack.OAuthService, nonce string) {
	flow, install, err := service.Pending(r.Context(), nonce, acct.ID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	app, err := s.store.AppByID(r.Context(), flow.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrValidation("app ownership changed; restart Slack consent"))
		return
	}
	csrf, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: csrf, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	view := dashboard.SlackOAuthConfirmation{AppSlug: app.Slug, State: nonce, CSRF: csrf, Installation: install}
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Confirm Slack workspace", Body: "issue_slack_oauth", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
func (s *server) dashboardSlackOAuthView(w http.ResponseWriter, r *http.Request, app state.App, view *dashboard.IssueTrackerNotificationsView) bool {
	service, ok := s.slackOAuthService()
	if !ok {
		return true
	}
	install, err := service.Store.GetIssueSlackInstallation(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	view.SlackOAuthAvailable = service.Config.Enabled()
	view.SlackInstallation = install
	if install.Status == "connected" {
		ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackOAuthTimeout)
		defer cancel()
		view.SlackChannels, err = service.Channels(ctx, app.ID, app.AccountID)
		view.SlackChannelLookupFailed = err != nil
	}
	return true
}

func (s *server) applyDashboardSlackOAuth(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, service issueslack.OAuthService, revision int64) error {
	ctx := r.Context()
	action := r.PostForm.Get("settings_kind")
	var err error
	switch action {
	case "slack_oauth_disconnect":
		err = service.Store.DisconnectIssueSlackOAuth(ctx, app.ID, acct.ID, revision)
	case "slack_oauth_finish":
		nonce := r.PostForm.Get("state")
		if !slackOAuthNonce(r, nonce) {
			return api.ErrValidation("Slack authorization state did not match; restart consent")
		}
		err = service.Finish(ctx, nonce, app.ID, acct.ID)
		if err == nil {
			setSlackOAuthCookie(w, "")
		}
	default:
		destinationRevision, e := strconv.ParseInt(r.PostForm.Get("destination_revision"), 10, 64)
		if e != nil {
			return api.ErrValidation("reload channel settings")
		}
		err = service.SetDestination(ctx, app.ID, acct.ID, state.PutIssueSlackDestination{TeamID: r.PostForm.Get("team_id"), ChannelID: r.PostForm.Get("channel_id"), Enabled: r.PostForm.Get("enabled") == "1", Revision: destinationRevision}, revision)
	}
	return err
}
