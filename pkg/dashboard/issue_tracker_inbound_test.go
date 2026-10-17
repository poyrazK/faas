package dashboard

import (
	"bytes"

	"html/template"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
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
			if !strings.Contains(body, "Inbound sync preview") || !strings.Contains(body, template.HTMLEscapeString(view.InboundReason)) || !strings.Contains(body, "Automatic inbound reopenings require an explicitly enabled") {
				t.Fatal(body)
			}
			if strings.Contains(body, `value="pull"`) || strings.Contains(body, `value="apply"`) {
				t.Fatal("preview authorized a write")
			}
		})
	}
}

func TestInboundPullFormRequiresGuardAndDeploymentSelection(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, lifecycle := range []string{"open", "resolved"} {
		t.Run(lifecycle, func(t *testing.T) {
			view := IssueTrackerSyncPreview{Provider: "linear", CanPull: true, InboundOutcome: "candidate", InboundGuardRevision: 71, ExpectedJSON: `{"value":"<script>attack</script>"}`, FixingDeployments: []IssueTrackerDeploymentSuggestion{{IssueTrackerFixingDeployment: state.IssueTrackerFixingDeployment{ID: "deployment", CommitSHA: "<script>commit</script>"}}}}
			view.Status.State = lifecycle
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "issue_tracker_sync.html", Page{Data: view}); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			for _, text := range []string{`value="pull"`, `name="inbound_guard_revision" value="71"`, "pause sync"} {
				if !strings.Contains(body, text) {
					t.Fatal(body)
				}
			}
			if strings.Contains(body, `<script>attack</script>`) || strings.Contains(body, `<script>commit</script>`) {
				t.Fatal("unescaped pull evidence")
			}
			if strings.Contains(body, `name="fixed_deployment_id"`) != (lifecycle == "resolved") {
				t.Fatal("deployment selection shown for wrong action", body)
			}
		})
	}
}
