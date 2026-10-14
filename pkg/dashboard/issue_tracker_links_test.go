package dashboard

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"html/template"
	"strings"
	"testing"
)

func TestIssueTrackerLinksEscapeTitlesAndProtectMutations(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	view := IssueTrackerLinksView{AppSlug: "payments", IssueID: "group", CSRF: "token", NextURL: "/dashboard/apps/payments/issues?issue=group&tracker_link_cursor=next", Items: []api.IssueTrackerLink{{ID: "link-one", OriginalIssueID: "original", Provider: "github", ExternalKey: "owner/repo#42", URL: "https://github.com/owner/repo/issues/42", Title: "<script>attack</script>"}}}
	if err := tmpl.ExecuteTemplate(&out, "issue-tracker-links", view); err != nil {
		t.Fatal(err)
	}
	html := out.String()
	if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") {
		t.Fatal("tracker title was not escaped")
	}
	for _, want := range []string{`rel="noopener noreferrer"`, `action="/dashboard/apps/payments/issues/group/tracker-links"`, `name="link_id" value="link-one"`, `name="action" value="delete"`, `name="action" value="save"`, `original issue <code>original</code>`, `tracker_link_cursor=next`} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Count(html, `name="csrf_token" value="token"`) != 2 {
		t.Fatal("both mutations require CSRF")
	}
}

func TestIssueTrackerSyncControlsRespectOwnershipAndShowPendingOutcome(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	view := IssueTrackerLinksView{AppSlug: "payments", IssueID: "group", CSRF: "csrf", CanManage: true, Items: []api.IssueTrackerLink{{ID: "link", Provider: "linear", URL: "https://linear.app/acme/issue/PAY-42"}}, Policies: map[string]api.IssueTrackerAutoSync{"link": {LinkID: "link", Enabled: true, Revision: 7, PendingState: "resolved", Outcome: "error"}}, Reasons: map[string]string{"link": IssueTrackerSyncReason("pending_outcome_unconfirmed")}, LinearConnection: &api.IssueTrackerLinearConnection{Connected: true, Revision: 3, TeamID: "team"}}
	for _, owner := range []bool{true, false} {
		view.CanManage = owner
		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "issue-tracker-links", view); err != nil {
			t.Fatal(err)
		}
		rendered := out.String()
		if !strings.Contains(rendered, "Waiting to confirm: resolved") || !strings.Contains(rendered, "observing it without sending it again") {
			t.Fatal("pending recovery guidance missing")
		}
		if owner {
			for _, want := range []string{"Review tracker sync", "Disable automatic sync", `name="expected_revision" value="7"`, `type="password" name="api_key"`, "Disconnect Linear"} {
				if !strings.Contains(rendered, want) {
					t.Fatal("owner control missing", want)
				}
			}
		} else if strings.Contains(rendered, "Review tracker sync") || strings.Contains(rendered, `name="api_key"`) {
			t.Fatal("non-owner can manage provider access")
		}
	}
}
func TestIssueTrackerPreviewEscapesSnapshotAndRequiresAlignment(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync.html")
	if err != nil {
		t.Fatal(err)
	}
	view := IssueTrackerSyncPreview{AppSlug: "payments", IssueID: "group", LinkID: "link", Provider: "linear", CSRF: "csrf", SourceState: "open", Aligned: true, ExpectedJSON: `{"value":"<script>attack</script>"}`}
	for _, aligned := range []bool{true, false} {
		view.Aligned = aligned
		view.CanPush = !aligned
		var out bytes.Buffer
		if err := tmpl.ExecuteTemplate(&out, "issue_tracker_sync.html", Page{Data: view}); err != nil {
			t.Fatal(err)
		}
		rendered := out.String()
		if strings.Contains(rendered, "<script>") {
			t.Fatal("snapshot escaped incorrectly")
		}
		if aligned {
			for _, want := range []string{`name="linear_open_state_id"`, `name="linear_resolved_state_id"`, `name="action" value="enable"`, `&lt;script&gt;`} {
				if !strings.Contains(rendered, want) {
					t.Fatal("preview mapping missing", want)
				}
			}
		} else if strings.Contains(rendered, `name="action" value="enable"`) || !strings.Contains(rendered, `name="action" value="push"`) {
			t.Fatal("unaligned preview delegates automatic sync")
		}
	}
}
