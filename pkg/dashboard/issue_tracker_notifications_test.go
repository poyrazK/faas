package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrackerNotificationInboxSeparatesOwnerControlsAndEscapesEvidence(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_notifications.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []bool{false, true} {
		v := IssueTrackerNotificationsView{AppSlug: "app", CanManage: owner, FailureThresholdMax: 20, Policy: state.IssueTrackerNotificationPolicy{FailureThreshold: 3}, Items: []state.IssueTrackerSyncNotification{{ID: "notice", Kind: "paused", IssueID: "issue", LinkID: "link", IssueTitle: "<script>issue</script>", TeamName: "<script>team</script>"}}, Reasons: map[string]string{"notice": "<script>reason</script>"}}
		var out bytes.Buffer
		if err = tmpl.ExecuteTemplate(&out, "issue_tracker_notifications.html", Page{Data: v}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		if strings.Contains(body, "<script>") || strings.Contains(body, `name="failure_threshold"`) != owner || strings.Contains(body, `name="interval_minutes"`) != owner || !strings.Contains(body, "Sync health and history") {
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
