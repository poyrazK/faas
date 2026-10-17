package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestResponseInboxControlsAndEscaping(t *testing.T) {
	now := time.Now().UTC()
	v := NewIssueApplicationResponseInboxView("app", "csrf", state.IssueApplicationResponseFilter{Status: "escalated", MyTeams: true}, state.IssueApplicationResponseInbox{NextID: "next", Items: []state.IssueApplicationResponseItem{{IssueID: "issue", Environment: "<script>evidence</script>", Revision: 7, Escalated: true, ResponsibleTeamName: "<script>team</script>", RoutedTeamName: "Backup", Outcome: "unrouted", ReminderDueAt: &now, EscalationDueAt: &now}}})
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_application_response_inbox.html")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err = tmpl.ExecuteTemplate(&out, "issue_application_response_inbox.html", Page{Data: v}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	for _, text := range []string{"Acknowledge", "Snooze", "unrouted", "Next page", "value=\"7\"", "team=mine", "Next reminder eligible", "Backup escalation eligible"} {
		if !strings.Contains(body, text) {
			t.Fatal(text, body)
		}
	}
	if strings.Contains(body, "<script>") || strings.Contains(body, "Open issue evidence") {
		t.Fatal(body)
	}
	// Acknowledgment forms must not submit the snooze input.
	first := strings.Split(strings.Split(body, "<form method=\"post\"")[1], "</form>")[0]
	if strings.Contains(first, "snoozed_until") {
		t.Fatal("acknowledgment includes snooze fields")
	}
}
