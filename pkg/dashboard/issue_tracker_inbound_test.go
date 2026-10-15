package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestInboundAssessmentRendersWithoutApplyControl(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{"fixing_deployment_required", "source_changed", "outbound_pending", "workflow_unmapped", "binding_changed"} {
		t.Run(reason, func(t *testing.T) {
			var out bytes.Buffer
			view := IssueTrackerSyncPreview{Provider: "github", SourceState: "open", InboundOutcome: "blocked", InboundReason: IssueTrackerInboundReason(reason)}
			if err := tmpl.ExecuteTemplate(&out, "issue_tracker_sync.html", Page{Data: view}); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			if !strings.Contains(body, "Inbound sync preview") || !strings.Contains(body, template.HTMLEscapeString(view.InboundReason)) || !strings.Contains(body, "does not apply changes or enable inbound sync") {
				t.Fatal(body)
			}
			if strings.Contains(body, `value="pull"`) || strings.Contains(body, `value="apply"`) {
				t.Fatal("preview authorized a write")
			}
		})
	}
}
