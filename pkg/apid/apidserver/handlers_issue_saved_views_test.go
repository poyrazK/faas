//go:build !no_pg

package apidserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1460
func TestIssueSavedViewsAndSymbolicatedOwnershipRoutes(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "saved-view-routing")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + token.Token}
	base := "/v1/apps/" + app.Slug
	rules := api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{{SourcePathPrefix: "/app/src/payments", Tag: "component=payments", AssigneeAccountID: app.AccountID}}}
	w := e.Do(t, "PUT", base+"/issue-ownership-rules", rules, nil)
	saved := apidserver.IssueDecode[api.IssueOwnershipRules](t, w, 200)
	if saved.Rules[0].SourcePathPrefix != rules.Rules[0].SourcePathPrefix || saved.Rules[0].Tag != rules.Rules[0].Tag {
		t.Fatal("new matchers lost on policy write")
	}
	manifest := api.PublishIssueSourceMapsRequest{Directory: "/app/dist", Manifest: api.IssueSourceMapManifest{Version: 1, Artifacts: []api.IssueSourceMapArtifact{{GeneratedFile: "bundle.js", SHA256: strings.Repeat("a", 64), Payload: `{"version":3,"sourceRoot":"../","sources":["src/payments/charge.ts"],"names":["charge"],"mappings":"AASAA"}`}}}}
	_ = apidserver.IssueDecode[api.IssueSourceMapsMetadata](t, e.Do(t, "PUT", base+"/deployments/"+dep.ID+"/issue-source-maps", manifest, nil), 200)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "PaymentError", Message: "failed", StackTrace: "PaymentError: failed\n    at a (/app/dist/bundle.js:1:1)", GroupingStrategy: "failure-site-v1", SymbolicationStrategy: "deployment-source-maps-v1", Tags: map[string]string{"component": "payments"}}
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", event, reporter), 202)
	detail := apidserver.IssueDecode[api.IssueDetail](t, e.Do(t, "GET", base+"/issues/"+accepted.IssueID, nil, nil), 200)
	if detail.Issue.AssigneeAccountID != app.AccountID || detail.Events[0].Frames[0].File != "/app/src/payments/charge.ts" {
		t.Fatalf("ownership evaluated unmapped evidence: %+v", detail)
	}
	in := api.PutIssueSavedViewRequest{Name: " My payments ", Filters: api.IssueSavedViewFilters{State: "open", Assignee: "me", Sort: "impact", Tag: "component=payments"}}
	path := base + "/issue-saved-views/mine"
	for _, method := range []string{"PUT", "GET", "DELETE"} {
		if w := e.Do(t, method, path, in, reporter); w.Code != 401 && w.Code != 403 {
			t.Fatalf("reporter gained view authority: %s %d", method, w.Code)
		}
	}
	view := apidserver.IssueDecode[api.IssueSavedView](t, e.Do(t, "PUT", path, in, nil), 200)
	if view.Name != "My payments" || view.Filters.Assignee != "me" || view.UpdatedByAccountID != app.AccountID {
		t.Fatalf("saved relative view: %+v", view)
	}
	listed := apidserver.IssueDecode[api.ListIssueSavedViewsResponse](t, e.Do(t, "GET", base+"/issue-saved-views", nil, nil), 200)
	if len(listed.Items) != 1 {
		t.Fatal("saved view missing")
	}
	issues := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", base+"/issues?"+view.Filters.Query().Encode(), nil, nil), 200)
	if len(issues.Items) != 1 || issues.Items[0].ID != accepted.IssueID {
		t.Fatal("saved me/tag filters did not apply")
	}
	bad := in
	bad.Filters.Tag = "email=private@example.com"
	if w := e.Do(t, "PUT", path, bad, nil); w.Code != 400 {
		t.Fatalf("unknown tag persisted: %d", w.Code)
	}
	foreign := apidserver.SeedPGApp(t, e, "foreign-saved-view")
	if w := e.Do(t, "GET", "/v1/apps/"+foreign.Slug+"/issue-saved-views/mine", nil, nil); w.Code != 404 {
		t.Fatalf("shared view escaped its app: %d", w.Code)
	}
	if w := e.Do(t, "DELETE", path, nil, nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 404 {
		t.Fatal("deleted view still exists")
	}
}
