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

func (s *server) dashboardSlackInteractionAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) {
	st, ok := s.store.(state.IssueSlackInteractionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Slack interactions unavailable"))
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("expected_revision"), 10, 64)
	in := state.IssueSlackInteractionPolicy{Enabled: r.PostForm.Get("enabled") == "1", Revision: revision, WorkspaceID: r.PostForm.Get("workspace_id"), SlackAppID: r.PostForm.Get("slack_app_id")}
	if err == nil && r.PostForm.Get("signing_secret") != "" {
		secret := r.PostForm.Get("signing_secret")
		if !issueslack.ValidSigningSecret(secret) || setSecretRecipient == nil || setSecretRecipient() == nil {
			err = state.ErrInvalidArgument
		} else {
			in.SecretSealed, err = secretbox.SealBytes(setSecretRecipient(), state.IssueSlackSigningSecretNamespace, []byte(secret), api.IssueSlackSigningSecretBytes)
		}
	}
	if err == nil {
		_, err = st.SetIssueSlackInteractionPolicy(r.Context(), app.ID, acct.ID, in)
	}
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/tracker-notifications?saved=slack-interactions#slack-interaction-settings", http.StatusSeeOther)
}
func (s *server) dashboardSlackInteractionView(w http.ResponseWriter, r *http.Request, app state.App, view *dashboard.IssueTrackerNotificationsView) bool {
	st, ok := s.store.(state.IssueSlackInteractionStore)
	if !ok {
		return true
	}
	p, err := st.GetIssueSlackInteractionPolicy(r.Context(), app.ID, app.AccountID)
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	view.SlackInteractionsAvailable = true
	view.SlackInteractionPolicy = p
	view.SlackInteractionURL = s.cliAuthURLBase + "/integrations/slack/issues/" + app.ID
	if view.SlackInstallation.Status == "connected" {
		view.SlackInteractionURL = s.cliAuthURLBase + "/integrations/slack/actions"
	}
	return true
}
func (s *server) dashboardSlackAccountLinkAction(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) bool {
	action := r.PostForm.Get("action")
	if action != "link_slack" && action != "unlink_slack" {
		return false
	}
	st, ok := s.store.(state.IssueSlackInteractionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Slack account linking unavailable"))
		return true
	}
	var err error
	if action == "link_slack" {
		err = st.ConfirmIssueSlackAccountLink(r.Context(), app.ID, app.AccountID, acct.ID, r.PathValue("issue_id"), r.PostForm.Get("slack_link"))
	} else {
		err = st.DeleteIssueSlackAccountLink(r.Context(), app.ID, acct.ID)
	}
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return true
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/issues/"+url.PathEscape(r.PathValue("issue_id"))+"/triage", http.StatusSeeOther)
	return true
}
func (s *server) dashboardSlackAccountLinkView(w http.ResponseWriter, r *http.Request, app state.App, actor string, view *dashboard.IssueApplicationTriageView) bool {
	st, ok := s.store.(state.IssueSlackInteractionStore)
	if !ok {
		return true
	}
	link, err := st.GetIssueSlackAccountLink(r.Context(), app.ID, app.AccountID, actor, view.Triage.IssueID, r.URL.Query().Get("slack_link"))
	if err != nil {
		writeNativeIssueTrackerError(w, err)
		return false
	}
	view.SlackLink = link
	return true
}
