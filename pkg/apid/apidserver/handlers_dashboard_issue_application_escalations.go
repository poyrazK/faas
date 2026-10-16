package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
)

func (s *server) dashboardApplicationEscalationAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	st, ok := s.store.(state.IssueApplicationEscalationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("native escalation unavailable"))
		return
	}
	in, err := issuetriage.ParseEscalationPolicy(r.PostForm)
	if err == nil {
		_, err = st.SetIssueApplicationEscalationPolicy(r.Context(), app.ID, acct.ID, in)
	}
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=application-escalation#application-escalation-settings", http.StatusSeeOther)
}
func (s *server) dashboardApplicationEscalationView(w http.ResponseWriter, r *http.Request, app state.App, view *dashboard.IssueTrackerNotificationsView) bool {
	st, ok := s.store.(state.IssueApplicationEscalationStore)
	if !ok {
		return true
	}
	policy, err := st.GetIssueApplicationEscalationPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	view.ApplicationEscalationAvailable = true
	view.ApplicationEscalationPolicy = policy
	view.ApplicationEscalationSaved = r.URL.Query().Get("saved") == "application-escalation"
	view.ApplicationEscalationMin = api.IssueApplicationEscalationMinMinutes
	view.ApplicationEscalationMax = api.IssueApplicationEscalationMaxMinutes
	return s.dashboardSlackInteractionView(w, r, app, view)
}
