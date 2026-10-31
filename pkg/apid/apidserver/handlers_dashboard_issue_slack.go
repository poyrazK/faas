package apidserver

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issueslack"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) dashboardTrackerSlackAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) bool {
	kind := r.PostForm.Get("settings_kind")
	if kind == "slack_routing" {
		s.dashboardSlackRoutingAction(w, r, acct, app)
		return true
	}
	if kind == "slack_oauth_start" || kind == "slack_oauth_finish" || kind == "slack_oauth_disconnect" || kind == "slack_oauth_destination" {
		s.dashboardSlackOAuthAction(w, r, acct, app)
		return true
	}
	if kind == "slack_interactions" {
		s.dashboardSlackInteractionAction(w, r, acct, app)
		return true
	}
	if kind == "application_escalation" {
		s.dashboardApplicationEscalationAction(w, r, acct, app)
		return true
	}
	if kind == "application_reminders" {
		s.dashboardApplicationReminderAction(w, r, acct, app)
		return true
	}
	if kind == "application_slack" {
		s.dashboardApplicationSlackAction(w, r, acct, app)
		return true
	}
	if kind != "slack_destination" && kind != "slack_retry" {
		return false
	}
	st, ok := s.store.(state.IssueSlackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("team Slack destinations unavailable"))
		return true
	}
	var err error
	if kind == "slack_retry" {
		err = st.RetryIssueSlackDelivery(r.Context(), app.ID, acct.ID, r.PostForm.Get("delivery_id"))
	} else {
		err = s.saveIssueSlackDestination(r, st, app, acct)
	}
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return true
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=slack#slack-settings", http.StatusSeeOther)
	return true
}
func (s *server) saveIssueSlackDestination(r *http.Request, st state.IssueSlackStore, app state.App, acct state.Account) error {
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	if err != nil {
		return api.ErrValidation("reload Slack destination settings")
	}
	in := state.PutIssueSlackDestination{TeamID: r.PostForm.Get("team_id"), ChannelID: r.PostForm.Get("channel_id"), Revision: revision, Enabled: r.PostForm.Get("enabled") == "1"}
	token := r.PostForm.Get("bot_token")
	if token != "" {
		if !issueslack.ValidBotToken(token) {
			return api.ErrValidation("use a Slack bot token within the supported size limit")
		}
		recipients, recipientErr := accountRecipients(hostSecretRecipient())
		if recipientErr != nil {
			return api.ErrCapacity("Slack token sealing unavailable")
		}
		in.TokenSealed, err = secretbox.SealBytesTo(recipients, state.IssueSlackTokenNamespace, []byte(token), api.IssueSlackTokenMaxBytes)
		if err != nil {
			return api.ErrCapacity("could not seal Slack bot token")
		}
	}
	_, err = st.SetIssueSlackDestination(r.Context(), app.ID, acct.ID, in)
	return err
}
func (s *server) dashboardIssueSlackView(w http.ResponseWriter, r *http.Request, app state.App, view *dashboard.IssueTrackerNotificationsView) bool {
	if !view.CanManage {
		return true
	}
	st, ok := s.store.(state.IssueSlackStore)
	if !ok {
		return true
	}
	destinations, err := st.ListIssueSlackDestinations(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	for _, team := range view.Teams {
		setting := dashboard.IssueSlackTeamSettings{TeamID: team.ID, TeamName: team.Name}
		for _, d := range destinations {
			if d.TeamID == team.ID {
				setting.Destination = d
				break
			}
		}
		view.SlackTeams = append(view.SlackTeams, setting)
	}
	view.SlackDeliveries, err = st.ListIssueSlackDeliveries(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	if !s.dashboardSlackOAuthView(w, r, app, view) {
		return false
	}
	view.SlackSaved = r.URL.Query().Get("saved") == "slack"
	view.SlackTokenMax = api.IssueSlackTokenMaxBytes
	return s.dashboardApplicationSlackView(w, r, app, view)
}
