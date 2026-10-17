package dashboard

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/state"
	"html/template"
	"strings"
	"testing"
	"time"
)

func TestApplicationResponseControlsAndEscaping(t *testing.T) {
	until := time.Now().UTC().Add(time.Hour)
	for _, active := range []bool{true, false} {
		v := NewIssueApplicationTriageView("app", "csrf", state.IssueApplicationTriage{IssueID: "issue", EscalatedAt: &until, ResponsibleTeamName: "<script>backup</script>", Escalations: []state.IssueApplicationReminder{{Kind: "escalation", TeamName: "<script>target</script>", Outcome: "queued"}}, Active: active, Snoozed: active, SnoozedUntil: &until, SnoozedName: "<script>member</script>", SnoozeReason: "<script>reason</script>", Reminders: []state.IssueApplicationReminder{{Outcome: "queued", TeamName: "<script>reminder-team</script>", Sequence: 1}}, History: []state.IssueApplicationTriageActivity{{ActorName: "<script>history</script>", Action: "snooze", Reason: "<script>audit</script>"}}}, false)
		tmpl, err := template.ParseFS(tmplFS, "templates/issue_application_triage.html")
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "issue_application_triage.html", Page{Data: v}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		if strings.Contains(body, "<script>") || strings.Contains(body, `name="expected_revision"`) != active || strings.Contains(body, "Open issue evidence") {
			t.Fatal(body)
		}
	}
}
