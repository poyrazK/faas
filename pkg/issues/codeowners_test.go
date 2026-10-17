package issues

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCodeownersPriorityAndTeamBindings(t *testing.T) {
	account, team := uuid.NewString(), uuid.NewString()
	var bindings map[string]CodeownerTarget
	if err := json.Unmarshal([]byte(`{"@alice":"`+account+`","@org/payments":{"team_id":"`+team+`"}}`), &bindings); err != nil {
		t.Fatal(err)
	}
	out, err := CompileCodeownersTargets("# owners\n* @alice\n/src/payments/ @ORG/PAYMENTS @alice # comment\n", "/app", bindings)
	if err != nil || len(out.Rules) != 2 || out.RuleLines[0] != 3 || out.Rules[0].AssigneeTeamID != team || out.Rules[1].AssigneeAccountID != account {
		t.Fatalf("import: %+v %v", out, err)
	}
	event := api.IssueEvent{Frames: []api.IssueFrame{{File: "/app/src/payments/charge.ts", InApp: true}}}
	if !OwnershipRuleMatches(out.Rules[0], event) || OwnershipRuleMatches(out.Rules[0], api.IssueEvent{Frames: []api.IssueFrame{{File: "/app/src/payments-other/x.ts", InApp: true}}}) {
		t.Fatal("compiled route lost path boundary")
	}
	if _, err := CompileCodeowners("/src/main.go @alice", "/app", map[string]string{"@alice": account}); err != nil {
		t.Fatal(err)
	}
}
func TestCodeownersRejectsUnrepresentableInputs(t *testing.T) {
	id := uuid.NewString()
	bindings := map[string]CodeownerTarget{"@alice": {AccountID: id}}
	for _, source := range []string{"*.go @alice", "/src/** @alice", "!src @alice", "/src/../secret @alice", "https://example.test/src @alice", "/src#fragment @alice", "/src @missing", "/src @alice @missing", "/src", "/src\\ path @alice", strings.Repeat("x", api.IssueCodeownersMaxBytes+1), string([]byte{255})} {
		t.Run(source[:min(len(source), 30)], func(t *testing.T) {
			if _, err := CompileCodeownersTargets(source, "/app", bindings); err == nil {
				t.Fatal("unsafe/unsupported policy accepted")
			}
		})
	}
	for _, target := range []CodeownerTarget{{}, {AccountID: id, TeamID: id}, {TeamID: "bad"}} {
		if _, err := CompileCodeownersTargets("* @alice", "/app", map[string]CodeownerTarget{"@alice": target}); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
	if _, err := CompileCodeownersTargets("* @alice", "/app", map[string]CodeownerTarget{"@alice": {AccountID: id}, "@ALICE": {TeamID: id}}); err == nil {
		t.Fatal("case conflict accepted")
	}
}
