package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"net/url"
	"strconv"
)

func (s *server) dashboardTrackerEscalationPolicy(w http.ResponseWriter, r *http.Request, acct state.Account, st state.IssueTrackerNotificationStore, app state.App) {
	revision, e1 := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	delay, e2 := strconv.Atoi(r.PostForm.Get("delay_minutes"))
	if e1 != nil || e2 != nil {
		api.WriteProblem(w, api.ErrValidation("reload escalation settings"))
		return
	}
	_, err := st.SetIssueTrackerEscalationPolicy(r.Context(), app.ID, acct.ID, state.IssueTrackerEscalationPolicy{Enabled: r.PostForm.Get("enabled") == "1", Revision: revision, DelayMinutes: delay, TeamID: r.PostForm.Get("team_id")})
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=escalation", http.StatusSeeOther)
}
