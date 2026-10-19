package dashboard

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestFixingDeploymentSuggestionsRequireAllEvidence(t *testing.T) {
	now := time.Now().UTC()
	choices := []state.IssueTrackerFixingDeployment{
		{ID: "older", Status: "live", CreatedAt: now.Add(-time.Hour), LastSeenAt: now},
		{ID: "building", Status: "building", CreatedAt: now.Add(time.Hour), LastSeenAt: now},
		{ID: "observed", Status: "live", CreatedAt: now.Add(time.Hour), LastSeenAt: now, ObservedEvents: 1},
		{ID: "unknown", Status: "live", CreatedAt: now.Add(time.Hour)},
		{ID: "candidate", Status: "live", CreatedAt: now.Add(time.Hour), LastSeenAt: now},
	}
	out := SuggestFixingDeployments(choices)
	if out[0].ID != "candidate" || !out[0].Suggested || choices[0].ID != "older" {
		t.Fatal(out, choices)
	}
	for _, d := range out[1:] {
		if d.Suggested {
			t.Fatal("unsupported suggestion", d)
		}
	}
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_sync.html")
	if err != nil {
		t.Fatal(err)
	}
	view := IssueTrackerSyncPreview{CanPull: true, InboundOutcome: "needs_deployment", FixingDeployments: out}
	view.Status.State = "resolved"
	var body bytes.Buffer
	if err = tmpl.ExecuteTemplate(&body, "issue_tracker_sync.html", Page{Data: view}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`value="">Choose the deployment`, "Suggested candidate", "Creation time is not rollout time", "do not prove a fix", "recorded occurrences"} {
		if !strings.Contains(body.String(), want) {
			t.Fatal(want, body.String())
		}
	}
	if strings.Contains(body.String(), `value="candidate" selected`) {
		t.Fatal("suggestion selected without review")
	}
}

func TestTrackerReviewQueueEscapesSavedEvidenceAndOnlyOffersPreview(t *testing.T) {
	tmpl, err := template.ParseFS(tmplFS, "templates/issue_tracker_review.html")
	if err != nil {
		t.Fatal(err)
	}
	view := IssueTrackerReviewView{AppSlug: "app", CSRF: "csrf", Category: "closures", Reasons: map[string]string{"link": "<script>reason</script>"}, Items: []state.IssueTrackerReview{{IssueID: "issue", LinkID: "link", Provider: "github", IssueTitle: "<script>issue</script>", TicketTitle: "<script>ticket</script>", Reason: "fixing_deployment_required", PendingState: "resolved", Regrouped: true}}}
	var body bytes.Buffer
	if err = tmpl.ExecuteTemplate(&body, "issue_tracker_review.html", Page{Data: view}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"does not contact providers", `value="preview"`, "unconfirmed", "regrouped", "review fixing deployment"} {
		if !strings.Contains(body.String(), want) {
			t.Fatal(want, body.String())
		}
	}
	if strings.Contains(body.String(), "<script>") || strings.Contains(body.String(), `value="pull"`) || strings.Contains(body.String(), `value="enable"`) {
		t.Fatal("unsafe queue", body.String())
	}
}
