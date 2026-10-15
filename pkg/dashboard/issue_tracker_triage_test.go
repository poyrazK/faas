package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrackerTriageControlsAndEscapedHistory(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_triage.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                     string
		active, claimed, release bool
		want, absent             string
	}{
		{"new", true, false, false, "Claim for me", "Release claim"},
		{"claimed by another", true, true, false, "Investigating:", "Release claim"},
		{"own claim", true, true, true, "Release claim", "Claim for me"},
		{"inactive", false, false, true, "no longer active", "name=\"action\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := IssueTrackerTriageView{CanRelease: tc.release, Triage: state.IssueTrackerTriage{Active: tc.active, AcknowledgedBy: "actor", AcknowledgedName: "<script>ack</script>", History: []state.IssueTrackerTriageActivity{{Action: "claim", ActorName: "<script>actor</script>"}}}}
			if tc.claimed {
				v.Triage.ClaimedBy = "other"
			}
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "issue_tracker_triage.html", Page{Data: v}); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if !strings.Contains(body, tc.want) || strings.Contains(body, tc.absent) || strings.Contains(body, "<script>") || strings.Contains(body, `value="acknowledge"`) {
				t.Fatal(body)
			}
		})
	}
}

func TestTrackerSnoozeShowsActorExpiryAndEscapedReason(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_triage.html")
	if err != nil {
		t.Fatal(err)
	}
	until := time.Now().UTC().Add(time.Hour)
	for _, snoozed := range []bool{true, false} {
		v := NewIssueTrackerTriageView("app", "csrf", state.IssueTrackerTriage{Active: true, Snoozed: snoozed, Resurfaced: !snoozed, SnoozedUntil: &until, SnoozedBy: "actor", SnoozedName: "<script>actor</script>", SnoozeReason: "<script>reason</script>", History: []state.IssueTrackerTriageActivity{{Action: "snooze", SnoozedUntil: &until, Reason: "<script>history</script>"}, {Action: "unsnooze"}}}, false)
		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "issue_tracker_triage.html", Page{Data: v}); err != nil {
			t.Fatal(err)
		}
		body := out.String()
		if strings.Contains(body, "<script>") || strings.Contains(body, `value="unsnooze"`) != snoozed || !strings.Contains(body, "Snooze until (UTC)") || (!snoozed && !strings.Contains(body, "Returned from snooze")) {
			t.Fatal(body)
		}
	}
}

func TestTrackerReminderHistoryEscapesRoutingNames(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_triage.html")
	if err != nil {
		t.Fatal(err)
	}
	v := IssueTrackerTriageView{Triage: state.IssueTrackerTriage{Reminders: []state.IssueTrackerReminder{{Sequence: 2, CreatedAt: time.Now().UTC(), TeamName: "<script>team</script>"}}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "issue_tracker_triage.html", Page{Data: v}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if !strings.Contains(body, "Reminder 2") || strings.Contains(body, "<script>") {
		t.Fatal(body)
	}
}

func TestTrackerEscalationHistoryAndCurrentResponsibilityEscapeTeams(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_triage.html")
	if err != nil {
		t.Fatal(err)
	}
	v := IssueTrackerTriageView{Triage: state.IssueTrackerTriage{Active: true, Escalated: true, ResponsibleTeamName: "<script>backup</script>", Escalations: []state.IssueTrackerEscalation{{TeamName: "<script>history</script>", CreatedAt: time.Now().UTC()}}}}
	var out bytes.Buffer
	if err = tmpl.ExecuteTemplate(&out, "issue_tracker_triage.html", Page{Data: v}); err != nil {
		t.Fatal(err)
	}
	body := out.String()
	if strings.Contains(body, "<script>") || !strings.Contains(body, "Current notification responsibility:") || !strings.Contains(body, "Escalation history") {
		t.Fatal(body)
	}
}
