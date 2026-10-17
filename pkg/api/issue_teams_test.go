package api

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestIssueTeamsValidateMembersAndAssignments(t *testing.T) {
	id := uuid.NewString()
	if out, err := NormalizeIssueTeam(PutIssueTeamRequest{Name: " Payments ", MemberAccountIDs: []string{id}}); err != nil || out.Name != "Payments" {
		t.Fatalf("valid roster: %+v %v", out, err)
	}
	for _, in := range []PutIssueTeamRequest{{Name: "x"}, {Name: "x", MemberAccountIDs: []string{id, id}}, {Name: "x", MemberAccountIDs: []string{uuid.Nil.String()}}, {Name: "x", MemberAccountIDs: []string{"bad"}}} {
		if _, err := NormalizeIssueTeam(in); err == nil {
			t.Fatal("invalid roster accepted")
		}
	}
	if _, err := NormalizeIssueOwnershipRules([]IssueOwnershipRule{{SourceKind: "exception", AssigneeTeamID: id}}); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizeIssueOwnershipRules([]IssueOwnershipRule{{SourceKind: "exception", AssigneeTeamID: id, AssigneeAccountID: id}}); err == nil {
		t.Fatal("ambiguous route accepted")
	}
	for _, assignee := range []string{"me", "unassigned", id} {
		if _, err := NormalizeIssueSavedView(PutIssueSavedViewRequest{Name: "x", Filters: IssueSavedViewFilters{Team: id, Assignee: assignee}}); err == nil {
			t.Fatal("incompatible saved filters accepted")
		}
	}
	view, err := NormalizeIssueSavedView(PutIssueSavedViewRequest{Name: "x", Filters: IssueSavedViewFilters{Team: id}})
	if err != nil || view.Filters.Query().Get("team") != id {
		t.Fatal("team view lost filter")
	}
	in := BulkIssueActionRequest{IssueIDs: []string{uuid.NewString()}, Action: IssueActionRequest{Action: "assign", AssigneeTeamID: id}}
	if _, err := NormalizeBulkIssueAction(in, time.Now()); err != nil {
		t.Fatal(err)
	}
	in.Action.Action = "reopen"
	if _, err := NormalizeBulkIssueAction(in, time.Now()); err == nil {
		t.Fatal("unrelated team field accepted")
	}
}
