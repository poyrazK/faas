package apidserver

import (
	"net/http"
	"net/url"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardApplicationSlackAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	st, ok := s.store.(state.IssueApplicationSlackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application Slack settings unavailable"))
		return
	}
	in, err := issueslack.ApplicationPolicyForm(r.PostForm)
	if err == nil {
		_, err = st.SetIssueApplicationSlackPolicy(r.Context(), app.ID, acct.ID, in)
	}
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=application-slack#application-slack-settings", http.StatusSeeOther)
}
func (s *server) dashboardApplicationSlackView(w http.ResponseWriter, r *http.Request, app state.App, view *dashboard.IssueTrackerNotificationsView) bool {
	st, ok := s.store.(state.IssueApplicationSlackStore)
	if !ok {
		return true
	}
	policy, err := st.GetIssueApplicationSlackPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	view.ApplicationSlackAvailable = true
	view.ApplicationSlackPolicy = policy
	view.ApplicationSlackSaved = r.URL.Query().Get("saved") == "application-slack"
	view.ApplicationSlackMaxDeliveries = api.IssueApplicationSlackMaxDeliveries
	for _, event := range []struct{ key, label string }{{"created", "New issues"}, {"regressed", "Regressions"}, {"impact_threshold_reached", "Customer-impact thresholds"}, {"frequency_threshold_reached", "Event-frequency thresholds"}, {"compound_threshold_reached", "Compound thresholds"}} {
		view.ApplicationSlackEvents = append(view.ApplicationSlackEvents, dashboard.IssueSlackEventChoice{Key: event.key, Label: event.label, Selected: slices.Contains(policy.Events, event.key)})
	}
	return s.dashboardApplicationReminderView(w, r, app, view)
}
