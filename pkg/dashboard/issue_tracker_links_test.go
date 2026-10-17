package dashboard

import (
	"bytes"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
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
	view.CallbackHealth = map[string]issuetracker.CallbackHealth{"link": {RetainedDeliveries: 3, PendingHints: 1}}
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
			for _, want := range []string{"Refresh provider status and review sync", "Disable automatic sync", `name="expected_revision" value="7"`, `type="password" name="api_key"`, "Disconnect Linear", "3 retained", "1 pending", "recent verified delivery confirms receipt", "Failed GitHub signatures"} {
				if !strings.Contains(rendered, want) {
					t.Fatal("owner control missing", want)
				}
			}
		} else if strings.Contains(rendered, "Refresh provider status and review sync") || strings.Contains(rendered, `name="api_key"`) {
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
		view.CanEnable = aligned
		view.CanPush = !aligned
		view.OpenWorkflows = []issuetracker.LinearWorkflowState{{ID: "open", Name: "Doing <script>attack</script>", Type: "started", State: "open"}}
		view.ResolvedWorkflows = []issuetracker.LinearWorkflowState{{ID: "done", Name: "Done", Type: "completed", State: "resolved"}}
		view.PushWorkflows = view.OpenWorkflows
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

func TestLinearPickerMatchesStoredIDsAndRequiresBothLifecycles(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"active", "missing resolved", "renamed", "missing selected"} {
		t.Run(kind, func(t *testing.T) {
			catalog := []issuetracker.LinearWorkflowState{{ID: "open", Name: "Doing", State: "open", Type: "started"}, {ID: "done", Name: "Done", State: "resolved", Type: "completed"}}
			view := IssueTrackerSyncPreview{Provider: "linear", Aligned: true, Policy: api.IssueTrackerAutoSync{LinearOpenStateID: "open", LinearResolvedStateID: "done"}}
			if kind == "missing resolved" {
				catalog = catalog[:1]
			}
			if kind == "renamed" {
				catalog[0].Name = "Building"
			}
			if kind == "missing selected" {
				view.Policy.LinearOpenStateID = "removed"
			}
			view.SetLinearWorkflowChoices(catalog)
			var out bytes.Buffer
			if err := tmpl.ExecuteTemplate(&out, "issue_tracker_sync.html", Page{Data: view}); err != nil {
				t.Fatal(err)
			}
			rendered := out.String()
			switch kind {
			case "missing resolved":
				if view.CanEnable || strings.Contains(rendered, `name="action" value="enable"`) {
					t.Fatal("incomplete catalog delegates sync")
				}
			case "active":
				if view.OpenWorkflowName != "Doing" || view.ResolvedWorkflowName != "Done" || !strings.Contains(rendered, `value="open" selected`) {
					t.Fatal("stored mapping selection missing")
				}
			case "renamed":
				if view.OpenWorkflowName != "Building" || strings.Contains(rendered, "Doing") {
					t.Fatal("stale provider name shown")
				}
			case "missing selected":
				if !strings.Contains(rendered, "unavailable; choose an active replacement") || strings.Contains(rendered, `value="removed"`) {
					t.Fatal("unavailable workflow offered")
				}
			}
		})
	}
}

func TestLinearCallbackDiagnosticsExplainScopeAndDoNotClaimRegistration(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issues.html")
	if err != nil {
		t.Fatal(err)
	}
	view := IssueTrackerLinksView{CanManage: true, Items: []api.IssueTrackerLink{{ID: "link", Provider: "linear"}}, CallbackHealth: map[string]issuetracker.CallbackHealth{"link": {Provider: "linear", Setup: "Configured; awaiting verified delivery", Configured: true, InvalidSignature: 2, WrongScope: 1, StaleDelivery: 3, InvalidPayload: 4, DeliveryConflict: 5}}}
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, "issue-tracker-links", view); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Configured; awaiting verified delivery", "invalid signature 2", "wrong scope 1", "stale delivery 3", "malformed payload 4", "reused delivery ID 5", "Counts include unauthenticated attempts and retries", "Check that the signing secret matches", "Check the webhook workspace and team", "Check Gregale", "not provider webhook registration"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("missing diagnostic guidance", want)
		}
	}
	if strings.Contains(out.String(), "Failed GitHub signatures") {
		t.Fatal("Linear diagnostics use wrong provider guidance")
	}
}

func TestLinearOAuthPickerEscapesTeamsAndKeepsCredentialsOutOfForms(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_linear_oauth.html")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	view := LinearOAuthPicker{AppSlug: "payments", IssueID: "issue", State: "nonce", CSRF: "csrf", WorkspaceID: "workspace", Teams: []issuetracker.LinearOAuthTeam{{ID: "team", Name: "Team <script>unsafe</script>"}}}
	if err = tmpl.ExecuteTemplate(&out, "issue_linear_oauth.html", Page{Data: view}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<!doctype html>`, `name="csrf_token" value="csrf"`, `name="state" value="nonce"`, `name="action" value="oauth_finish"`, `&lt;script&gt;`, `value="team"`} {
		if !strings.Contains(out.String(), want) {
			t.Fatal("picker contract missing", want)
		}
	}
	if strings.Contains(out.String(), "<script>") || strings.Contains(out.String(), `name="access_token"`) || strings.Contains(out.String(), `name="refresh_token"`) {
		t.Fatal("unsafe OAuth picker")
	}
}
