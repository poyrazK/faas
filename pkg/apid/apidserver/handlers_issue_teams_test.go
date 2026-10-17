//go:build !no_pg

package apidserver_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// adr: 1461
func TestIssueTeamManagementRoutingAndInbox(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	app := apidserver.SeedPGApp(t, e, "team-inbox")
	dep := apidserver.IssueSeedDeployment(t, e, app, 1)
	token := apidserver.IssueCreateToken(t, e, app.Slug, dep)
	reporter := map[string]string{"Authorization": "Bearer " + token.Token}
	base := "/v1/apps/" + app.Slug
	path := base + "/issue-teams/payments"
	in := api.PutIssueTeamRequest{Name: "Payments", MemberAccountIDs: []string{app.AccountID}}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if w := e.Do(t, method, path, in, reporter); w.Code != 401 && w.Code != 403 {
			t.Fatalf("reporter gained team access: %s %d", method, w.Code)
		}
	}
	team := apidserver.IssueDecode[api.IssueTeam](t, e.Do(t, "PUT", path, in, nil), 200)
	listed := apidserver.IssueDecode[api.ListIssueTeamsResponse](t, e.Do(t, "GET", base+"/issue-teams", nil, nil), 200)
	if len(listed.Items) != 1 || listed.Items[0].ID != team.ID {
		t.Fatal("team list lost identity")
	}
	rules := api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{{SourceKind: "exception", AssigneeTeamID: team.ID}}}
	_ = apidserver.IssueDecode[api.IssueOwnershipRules](t, e.Do(t, "PUT", base+"/issue-ownership-rules", rules, nil), 200)
	event := api.IssueEvent{EventID: uuid.NewString(), OccurredAt: time.Now().UTC(), ExceptionType: "PaymentError", Message: "failed", SourceKind: "exception"}
	accepted := apidserver.IssueDecode[api.IssueEventResponse](t, e.Do(t, "POST", base+"/issue-events", event, reporter), 202)
	inbox := apidserver.IssueDecode[api.ListIssuesResponse](t, e.Do(t, "GET", base+"/issues?team="+team.ID+"&state=open", nil, nil), 200)
	if len(inbox.Items) != 1 || inbox.Items[0].ID != accepted.IssueID || inbox.Items[0].AssigneeTeamID != team.ID {
		t.Fatal("team inbox did not route")
	}
	if w := e.Do(t, "GET", base+"/issues?team="+team.ID+"&assignee=unassigned", nil, nil); w.Code != 400 {
		t.Fatal("ambiguous inbox accepted")
	}
	if w := e.Do(t, "DELETE", path, nil, nil); w.Code != 409 {
		t.Fatalf("in-use delete: %d %s", w.Code, w.Body.String())
	}
	actionPath := base + "/issues/" + accepted.IssueID + "/actions"
	if w := e.Do(t, "POST", actionPath, api.IssueActionRequest{Action: "assign", AssigneeAccountID: app.AccountID, AssigneeTeamID: team.ID}, nil); w.Code != 400 {
		t.Fatal("ambiguous assignment accepted")
	}
	foreign := apidserver.SeedPGApp(t, e, "team-other-app")
	if w := e.Do(t, "PUT", "/v1/apps/"+foreign.Slug+"/issue-ownership-rules", rules, nil); w.Code != 400 {
		t.Fatalf("foreign team rule: %d", w.Code)
	}
	_ = apidserver.IssueDecode[api.Issue](t, e.Do(t, "POST", actionPath, api.IssueActionRequest{Action: "assign"}, nil), 200)
	_ = apidserver.IssueDecode[api.IssueOwnershipRules](t, e.Do(t, "PUT", base+"/issue-ownership-rules", api.IssueOwnershipRules{Rules: []api.IssueOwnershipRule{}}, nil), 200)
	if w := e.Do(t, "DELETE", path, nil, nil); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if w := e.Do(t, "GET", path, nil, nil); w.Code != 404 {
		t.Fatal("deleted team readable")
	}
}
