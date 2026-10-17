package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1493: native reminder settings are owner-only alongside existing controls.
func TestTrackerNotificationInboxSeparatesOwnerControlsAndEscapesEvidence(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_notifications.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []bool{false, true} {
		v := IssueTrackerNotificationsView{ApplicationEscalationAvailable: true, ApplicationEscalationPolicy: state.IssueApplicationEscalationPolicy{Enabled: true, DelayMinutes: 15, TeamID: "backup"}, ApplicationRemindersAvailable: true, ApplicationReminderPolicy: state.IssueApplicationReminderPolicy{Enabled: true, Revision: 4, IntervalMinutes: 60, RepeatLimit: 3}, AppSlug: "app", CanManage: owner, ApplicationSlackAvailable: true, ApplicationSlackPolicy: state.IssueApplicationSlackPolicy{Revision: 3, SkippedDeliveries: 2, UnroutedAlerts: 1}, ApplicationSlackEvents: []IssueSlackEventChoice{{Key: "created", Label: "New issues", Selected: true}}, SlackTeams: []IssueSlackTeamSettings{{TeamID: "team", TeamName: "<script>slack-team</script>"}}, SlackDeliveries: []state.IssueSlackDelivery{{ID: "delivery", TeamName: "<script>delivery</script>", Status: "failed", CanRetry: true}}, FailureThresholdMax: 20, Policy: state.IssueTrackerNotificationPolicy{FailureThreshold: 3}, Items: []state.IssueTrackerSyncNotification{{ID: "notice", Kind: "paused", IssueID: "issue", LinkID: "link", IssueTitle: "<script>issue</script>", TeamName: "<script>team</script>"}}, Reasons: map[string]string{"notice": "<script>reason</script>"}}
		var out bytes.Buffer
		if err = tmpl.ExecuteTemplate(&out, "issue_tracker_notifications.html", Page{Data: v}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		if strings.Contains(body, "<script>") || strings.Contains(body, `name="failure_threshold"`) != owner || strings.Contains(body, `name="interval_minutes"`) != owner || strings.Contains(body, `name="delay_minutes"`) != owner || strings.Contains(body, `name="bot_token"`) != owner || strings.Contains(body, `name="delivery_id"`) != owner || strings.Contains(body, `value="application_slack"`) != owner || strings.Contains(body, `value="application_reminders"`) != owner || strings.Contains(body, `value="application_escalation"`) != owner || strings.Contains(body, `name="repeat_limit"`) != owner || strings.Contains(body, `name="fallback_team_id"`) != owner || !strings.Contains(body, "Sync health and history") {
			t.Fatal(owner, body)
		}
	}
}

func TestMemberTrackerHealthCannotRequestProviderInspection(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync_health.html")
	if err != nil {
		t.Fatal(err)
	}
	v := IssueTrackerSyncHealthView{Provider: "github", CanManage: false}
	var out bytes.Buffer
	if err = tmpl.ExecuteTemplate(&out, "issue_tracker_sync_health.html", Page{Data: v}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), `value="preview"`) || !strings.Contains(out.String(), "Only the app owner") {
		t.Fatal(out.String())
	}
}
