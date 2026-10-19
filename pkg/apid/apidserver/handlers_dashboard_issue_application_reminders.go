package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
)

func (s *server) dashboardApplicationReminderAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	st, ok := s.store.(state.IssueApplicationReminderStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("native reminders unavailable"))
		return
	}
	in, err := issuetriage.ParseReminderPolicy(r.PostForm)
	if err == nil {
		_, err = st.SetIssueApplicationReminderPolicy(r.Context(), app.ID, acct.ID, in)
	}
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=application-reminders#application-reminder-settings", http.StatusSeeOther)
}
func (s *server) dashboardApplicationReminderView(w http.ResponseWriter, r *http.Request, app state.App, view *dashboard.IssueTrackerNotificationsView) bool {
	st, ok := s.store.(state.IssueApplicationReminderStore)
	if !ok {
		return true
	}
	policy, err := st.GetIssueApplicationReminderPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	view.ApplicationRemindersAvailable = true
	view.ApplicationReminderPolicy = policy
	view.ApplicationRemindersSaved = r.URL.Query().Get("saved") == "application-reminders"
	view.ApplicationReminderMin = api.IssueApplicationReminderMinMinutes
	view.ApplicationReminderMax = api.IssueApplicationReminderMaxMinutes
	view.ApplicationReminderRepeatMax = api.IssueApplicationRemindersMax
	return s.dashboardApplicationEscalationView(w, r, app, view)
}
