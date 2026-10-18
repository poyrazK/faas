package apidserver

import (
	"context"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardSlackRoutingHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := AccountFrom(r.Context()); !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesReadSurface...)(s.dashboardSlackRoutingForAccount)))(w, r)
}
func (s *server) dashboardSlackRoutingForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, app, ok := s.dashboardTrackerNotificationsAccess(w, r, acct)
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the app owner may manage Slack channel rules"))
		return
	}
	s.renderSlackRouting(w, r, acct, app, nil, state.IssueSlackRoutingSample{Event: "regressed", Environment: "production"}, nil)
}
func (s *server) dashboardSlackRoutingAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	service, ok := s.slackOAuthService()
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Slack channel rules unavailable"))
		return
	}
	if !acct.MayDeploy() {
		api.WriteProblem(w, api.ErrValidation("account cannot configure Slack access"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackOAuthTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	in, sample, err := issueslack.RoutingForm(r.PostForm)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	action := r.PostForm.Get("routing_action")
	if r.PostForm.Get("rule_move") != "" || action == "preview" {
		s.previewSlackRouting(w, r, acct, app, service, in, sample, action == "preview")
		return
	}
	if action != "save" {
		api.WriteProblem(w, api.ErrValidation("choose save or preview"))
		return
	}
	if _, err = service.SetRouting(ctx, app.ID, acct.ID, in); err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "issue.slack_routing_updated", &acct.ID, map[string]any{"app_id": app.ID, "rules": len(in.Rules)})
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/slack-routing?saved=1", http.StatusSeeOther)
}
func (s *server) previewSlackRouting(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, service issueslack.OAuthService, in state.PutIssueSlackRoutingPolicy, sample state.IssueSlackRoutingSample, preview bool) {
	var result *state.IssueSlackRoutingPreview
	if preview {
		out, err := service.PreviewRouting(r.Context(), app.ID, acct.ID, in, sample)
		if err != nil {
			writeNativeIssueTrackerError(w, err)
			return
		}
		result = &out
	} else {
		st, ok := s.store.(state.IssueSlackRoutingStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("Slack channel rules unavailable"))
			return
		}
		p, err := st.GetIssueSlackRoutingPolicy(r.Context(), app.ID, acct.ID)
		install, e := service.Store.GetIssueSlackInstallation(r.Context(), app.ID, acct.ID)
		if err != nil || e != nil || p.Revision != in.Revision || install.Status != "connected" || install.Revision != in.InstallationRevision {
			api.WriteProblem(w, api.ErrValidation("reload Slack channel rules before reordering"))
			return
		}
	}
	s.renderSlackRouting(w, r, acct, app, &in, sample, result)
}
func (s *server) renderSlackRouting(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, draft *state.PutIssueSlackRoutingPolicy, sample state.IssueSlackRoutingSample, preview *state.IssueSlackRoutingPreview) {
	ctx, cancel := context.WithTimeout(r.Context(), api.IssueSlackOAuthTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	view, err := s.slackRoutingView(r, app, draft, sample, preview)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	csrf, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	view.CSRF = csrf
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: csrf, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if err = dashboard.Render(w, s.log, httpsec.NonceFromContext(ctx), dashboard.Page{Title: "Slack channel rules", Body: "issue_slack_routing", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
func (s *server) slackRoutingView(r *http.Request, app state.App, draft *state.PutIssueSlackRoutingPolicy, sample state.IssueSlackRoutingSample, preview *state.IssueSlackRoutingPreview) (dashboard.IssueSlackRoutingView, error) {
	view := dashboard.IssueSlackRoutingView{AppSlug: app.Slug, Sample: sample, Preview: preview, Draft: draft != nil, Saved: r.URL.Query().Get("saved") == "1", MaxRules: api.IssueSlackRoutingMaxRules, KeyMaxBytes: api.IssueSlackRoutingKeyMaxBytes, EnvironmentMaxBytes: api.IssueSlackRoutingEnvironmentMaxBytes, TeamNames: map[string]string{}}
	st, ok := s.store.(state.IssueSlackRoutingStore)
	service, oauthOK := s.slackOAuthService()
	teams, teamOK := s.store.(state.IssueTeamStore)
	if !ok || !oauthOK || !teamOK {
		return view, api.ErrCapacity("Slack channel rules unavailable")
	}
	var err error
	view.Policy, err = st.GetIssueSlackRoutingPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		return view, err
	}
	view.Installation, err = service.Store.GetIssueSlackInstallation(r.Context(), app.ID, app.AccountID)
	if err != nil {
		return view, err
	}
	list, err := teams.ListIssueTeams(r.Context(), app.ID, app.AccountID)
	if err != nil {
		return view, err
	}
	view.Teams = list.Items
	for _, team := range view.Teams {
		view.TeamNames[team.ID] = team.Name
	}
	if draft != nil {
		view.Policy.Rules = draft.Rules
		view.Policy.Revision = draft.Revision
		view.Policy.InstallationRevision = draft.InstallationRevision
	}
	if view.Installation.Status == "connected" {
		view.Channels, err = service.Channels(r.Context(), app.ID, app.AccountID)
		view.ChannelLookupFailed = err != nil
	}
	return view, nil
}
