package apidserver

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardTrackerNotificationsAccess(w http.ResponseWriter, r *http.Request, acct state.Account) (state.IssueTrackerNotificationStore, state.App, bool) {

	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil {
		writeIssueError(w, err)
		return nil, app, false
	}
	st, ok := s.store.(state.IssueTrackerNotificationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("tracker notifications unavailable"))
		return nil, app, false
	}
	allowed, err := st.CanReadIssueTrackerNotifications(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("link_id"))
	if err != nil {
		writeIssueError(w, err)
		return nil, app, false
	}
	if !allowed {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Not found", "no such app"))
		return nil, app, false
	}
	owner := acct
	if acct.ID != app.AccountID {
		owner, err = s.store.AccountByID(r.Context(), app.AccountID)
		if err != nil {
			writeIssueError(w, err)
			return nil, app, false
		}
	}
	if _, ok = s.issueStore(w, owner); !ok {
		return nil, app, false
	}
	return st, app, true
}

func (s *server) dashboardTrackerNotificationsHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	st, app, ok := s.dashboardTrackerNotificationsAccess(w, r, acct)
	if !ok {
		return
	}
	cursor, err := state.DecodeIssueCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeIssueError(w, err)
		return
	}
	team := r.URL.Query().Get("team")
	page, err := st.ListIssueTrackerSyncNotificationsView(r.Context(), app.ID, app.AccountID, acct.ID, team, r.URL.Query().Get("view"), cursor)
	if err != nil {
		writeIssueError(w, err)
		return
	}
	policy := state.IssueTrackerNotificationPolicy{}
	reminderPolicy := state.IssueTrackerReminderPolicy{}
	if acct.ID == app.AccountID {
		policy, err = st.GetIssueTrackerNotificationPolicy(r.Context(), app.ID, app.AccountID)
		if err != nil {
			writeIssueError(w, err)
			return
		}
		reminderPolicy, err = st.GetIssueTrackerReminderPolicy(r.Context(), app.ID, app.AccountID)
		if err != nil {
			writeNativeIssueTrackerError(w, err)
			return
		}
	}
	s.renderDashboardTrackerNotifications(w, r, acct, app, team, policy, reminderPolicy, page)
}
func (s *server) renderDashboardTrackerNotifications(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, team string, policy state.IssueTrackerNotificationPolicy, reminderPolicy state.IssueTrackerReminderPolicy, page state.IssueTrackerNotificationPage) {
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, dashboardIssueAction, acct.ID, dashboardIssueCookie)
	if err != nil {
		renderProblem(w, s.log, err)
		return
	}
	// #nosec G124 -- configured production domains use Secure; empty domain supports local HTTP development.
	http.SetCookie(w, &http.Cookie{Name: dashboardIssueCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	escalation := state.IssueTrackerEscalationPolicy{}
	teams := api.ListIssueTeamsResponse{}
	if acct.ID == app.AccountID {
		st := s.store.(state.IssueTrackerNotificationStore)
		escalation, err = st.GetIssueTrackerEscalationPolicy(r.Context(), app.ID, app.AccountID)
		if err != nil {
			writeNativeIssueTrackerError(w, err)
			return
		}
		if issues, ok := s.store.(state.IssueTeamStore); ok {
			teams, err = issues.ListIssueTeams(r.Context(), app.ID, app.AccountID)
			if err != nil {
				writeIssueError(w, err)
				return
			}
		}
	}
	view := dashboard.IssueTrackerNotificationsView{EscalationPolicy: escalation, Teams: teams.Items, EscalationDelayMin: api.IssueTrackerEscalationDelayMinMinutes, EscalationDelayMax: api.IssueTrackerEscalationDelayMaxMinutes, EscalationSaved: r.URL.Query().Get("saved") == "escalation", AppSlug: app.Slug, CSRF: token, TeamFilter: team, InboxView: r.URL.Query().Get("view"), CanManage: app.AccountID == acct.ID, Policy: policy, ReminderPolicy: reminderPolicy, ReminderIntervalMin: api.IssueTrackerReminderIntervalMinMinutes, ReminderIntervalMax: api.IssueTrackerReminderIntervalMaxMinutes, RemindersMax: api.IssueTrackerRemindersMax, ReminderSaved: r.URL.Query().Get("saved") == "reminders", Items: page.Items, Reasons: map[string]string{}, Saved: r.URL.Query().Get("saved") == "1", FailureThresholdMax: api.IssueTrackerSyncNotificationFailuresMax}
	if !s.dashboardIssueSlackView(w, r, app, &view) {
		return
	}
	for _, item := range page.Items {
		view.Reasons[item.ID] = dashboard.IssueTrackerSyncReason(item.Reason)
	}
	if page.NextCursor != "" {
		view.NextURL = "/dashboard/apps/" + url.PathEscape(app.Slug) + "/issues/tracker-notifications?team=" + url.QueryEscape(team) + "&view=" + url.QueryEscape(view.InboxView) + "&cursor=" + url.QueryEscape(page.NextCursor)
	}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: "Tracker sync notifications", Body: "issue_tracker_notifications", Account: dashboardAccountView(acct, 0), Data: view}); err != nil {
		renderProblem(w, s.log, err)
	}
}
func (s *server) dashboardTrackerNotificationPolicyHandler(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.IssueEventMaxBytes)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardIssueAction, acct.ID, dashboardIssueCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload notifications"))
		return
	}
	s.authLimited(s.requireMFA(s.requireScope(api.ScopesDeployWriteSurface...)(s.dashboardTrackerNotificationPolicyForAccount)))(w, r)
}
func (s *server) dashboardTrackerNotificationPolicyForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	st, app, ok := s.dashboardTrackerNotificationsAccess(w, r, acct)
	if !ok {
		return
	}
	if app.AccountID != acct.ID {
		api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Owner required", "only the owner may configure sync notifications"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid notification settings"))
		return
	}
	if s.dashboardTrackerSlackAction(w, r, acct, app) {
		return
	}
	if r.PostForm.Get("settings_kind") == "escalation" {
		s.dashboardTrackerEscalationPolicy(w, r, acct, st, app)
		return
	}
	if r.PostForm.Get("settings_kind") == "reminders" {
		s.dashboardTrackerReminderPolicy(w, r, acct, st, app)
		return
	}
	revision, e1 := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	threshold, e2 := strconv.Atoi(r.PostForm.Get("failure_threshold"))
	if e1 != nil || e2 != nil {
		api.WriteProblem(w, api.ErrValidation("reload notification settings"))
		return
	}
	_, err := st.SetIssueTrackerNotificationPolicy(r.Context(), app.ID, acct.ID, state.IssueTrackerNotificationPolicy{Enabled: r.PostForm.Get("enabled") == "1", Revision: revision, FailureThreshold: threshold})
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=1", http.StatusSeeOther)
}

func (s *server) dashboardTrackerReminderPolicy(w http.ResponseWriter, r *http.Request, acct state.Account, st state.IssueTrackerNotificationStore, app state.App) {
	revision, e1 := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	interval, e2 := strconv.Atoi(r.PostForm.Get("interval_minutes"))
	if e1 != nil || e2 != nil {
		api.WriteProblem(w, api.ErrValidation("reload reminder settings"))
		return
	}
	_, err := st.SetIssueTrackerReminderPolicy(r.Context(), app.ID, acct.ID, state.IssueTrackerReminderPolicy{Enabled: r.PostForm.Get("enabled") == "1", Revision: revision, IntervalMinutes: interval})
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=reminders", http.StatusSeeOther)
}
