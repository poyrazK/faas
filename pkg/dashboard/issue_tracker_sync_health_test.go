package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTrackerRecoverySummaryDistinguishesAuthorityAndRetry(t *testing.T) {
	for _, tc := range []struct {
		enabled                bool
		outcome, pending, want string
	}{
		{true, "error", "", "Retryable check failure"},
		{true, "error", "resolved", "without sending it again"},
		{true, "blocked", "", "Paused for review"},
		{true, "conflict", "resolved", "Paused for review"},
		{false, "disabled", "", "disabled"},
		{true, "matched", "", "scheduled"},
	} {
		if got := IssueTrackerRecoverySummary(tc.enabled, tc.outcome, tc.pending); !strings.Contains(got, tc.want) {
			t.Fatal(tc, got)
		}
	}
}

func TestTrackerHealthRendersReceiptsWithoutDelegationControls(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync_health.html")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	health := state.IssueTrackerSyncHealth{LastSuccessfulAt: &now, History: []state.IssueTrackerSyncHistory{
		{ID: "checked", EventType: "checked", Outcome: "error", Reason: "provider_unavailable", PolicyRevision: 1, PendingState: "resolved", CreatedAt: now},
		{ID: "inspected", EventType: "inspected", Outcome: "conflict", Reason: "provider_unchanged", ObservedState: "open", PolicyRevision: 1, CreatedAt: now},
		{ID: "malformed", EventType: "<script>event</script>", Reason: "<script>reason</script>"},
	}}
	view := NewIssueTrackerSyncHealthView("app", "issue", "link", "github", "csrf", api.IssueTrackerAutoSync{Enabled: true, Outcome: "conflict", PendingState: "resolved"}, health)
	view.CanManage = true
	var body bytes.Buffer
	if err = tmpl.ExecuteTemplate(&body, "issue_tracker_sync_health.html", Page{Data: view}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Last successful automatic check", "Paused for review", "Check now and review", "policy authority unchanged", "Unconfirmed write", `name="action" value="preview"`, "provider still matches"} {
		if !strings.Contains(body.String(), want) {
			t.Fatal(want, body.String())
		}
	}
	if strings.Contains(body.String(), "<script>") || strings.Contains(body.String(), `value="enable"`) || strings.Contains(body.String(), `value="push"`) {
		t.Fatal("health page authorized writes", body.String())
	}
}
