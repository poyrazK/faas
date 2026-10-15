package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

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
