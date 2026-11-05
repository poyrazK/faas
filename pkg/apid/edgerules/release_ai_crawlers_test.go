package edgerules

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022083601 — amendment 1: a rule that tests an AI crawler field
// cannot be created, or switched on, until edge_ai_crawlers includes the
// account; clearing the condition or disabling the rule always works.
func TestAICrawlerConditionsNeedTheirFlag(t *testing.T) {
	acct := state.Account{ID: "acct"}
	aiMatch := &api.EdgeRuleMatchExpr{Field: "ai_crawler_purpose", Op: "eq", Value: "training"}
	create := api.CreateEdgeRuleRequest{Kind: "ip", Action: json.RawMessage(`{"deny":["0.0.0.0/0"]}`), Match: aiMatch}
	none := &Service{Host: releaseHost{}}
	released := &Service{Host: releaseHost{released: map[string]bool{platformflags.EdgeAICrawlers: true}}}
	if prob := none.createReleaseProblem(acct, create); prob == nil || prob.Code != api.CodeEdgeFeatureUnavailable {
		t.Fatalf("unreleased create = %v, want %s", prob, api.CodeEdgeFeatureUnavailable)
	}
	if prob := released.createReleaseProblem(acct, create); prob != nil {
		t.Fatalf("released create refused: %v", prob)
	}
	plain := create
	plain.Match = &api.EdgeRuleMatchExpr{Field: "country", Op: "eq", Value: "KP"}
	if prob := none.createReleaseProblem(acct, plain); prob != nil {
		t.Fatalf("a rule without AI fields refused: %v", prob)
	}

	off, on := false, true
	disabled := state.EdgeRule{Kind: state.EdgeRuleKindIP, Enabled: false, Match: aiMatch}
	enforced := state.EdgeRule{Kind: state.EdgeRuleKindIP, Enabled: true, Match: aiMatch}
	country := state.EdgeRule{Kind: state.EdgeRuleKindIP, Enabled: true, Match: plain.Match}
	for name, tc := range map[string]struct {
		row     state.EdgeRule
		req     api.UpdateEdgeRuleRequest
		refused bool
	}{
		"enable a disabled AI rule":         {disabled, api.UpdateEdgeRuleRequest{Enabled: &on}, true},
		"add an AI condition":               {country, api.UpdateEdgeRuleRequest{Match: aiMatch}, true},
		"edit an enforced AI rule":          {enforced, api.UpdateEdgeRuleRequest{Match: aiMatch}, false},
		"clear the AI condition":            {enforced, api.UpdateEdgeRuleRequest{ClearMatch: true}, false},
		"disable an enforced AI rule":       {enforced, api.UpdateEdgeRuleRequest{Enabled: &off}, false},
		"replace AI with another field":     {enforced, api.UpdateEdgeRuleRequest{Match: plain.Match}, false},
		"edit a disabled AI rule, stay off": {disabled, api.UpdateEdgeRuleRequest{Match: aiMatch}, false},
	} {
		if prob := none.updateReleaseProblem(acct, tc.row, tc.req); (prob != nil) != tc.refused {
			t.Errorf("%s: problem = %v, refused want %v", name, prob, tc.refused)
		}
	}
}
