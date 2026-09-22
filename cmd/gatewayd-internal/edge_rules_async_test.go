package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func asyncStateRule(id string, priority int, path string, methods ...string) state.EdgeRule {
	return state.EdgeRule{
		ID:           id,
		AccountID:    "acct_1",
		AppID:        "app_1",
		MatchPath:    path,
		MatchMethods: methods,
		Priority:     priority,
		Enabled:      true,
		Kind:         state.EdgeRuleKindAsync,
		Action: state.EdgeRuleAction{
			Kind:  state.EdgeRuleKindAsync,
			Async: &state.EdgeRuleAsyncAction{},
		},
	}
}

func TestCompileAsyncRulesFiltersSortsAndCompilesMethods(t *testing.T) {
	disabled := asyncStateRule("disabled", 0, "/reports", "POST")
	disabled.Enabled = false
	missingAction := asyncStateRule("missing-action", 1, "/reports", "POST")
	missingAction.Action.Async = nil
	otherKind := asyncStateRule("other", 2, "/reports", "POST")
	otherKind.Kind = state.EdgeRuleKindRoute

	rules, parseErrs := compileAsyncRules([]state.EdgeRule{
		asyncStateRule("later", 20, "/*"),
		disabled,
		missingAction,
		otherKind,
		asyncStateRule("first", 10, "/reports", "post", "PUT"),
	})
	if len(parseErrs) != 0 {
		t.Fatalf("parse errors = %v", parseErrs)
	}
	if len(rules) != 2 {
		t.Fatalf("compiled rules = %+v, want 2", rules)
	}
	if rules[0].ID != "first" || rules[1].ID != "later" {
		t.Fatalf("rule order = [%s %s], want [first later]", rules[0].ID, rules[1].ID)
	}
	if !rules[0].Methods["POST"] || !rules[0].Methods["PUT"] {
		t.Errorf("methods = %#v, want uppercase POST and PUT", rules[0].Methods)
	}
}

func TestCompileAsyncRulesDropsMalformedGlob(t *testing.T) {
	rules, parseErrs := compileAsyncRules([]state.EdgeRule{
		asyncStateRule("bad", 10, "[unclosed", "POST"),
	})
	if len(rules) != 0 || len(parseErrs) != 1 {
		t.Fatalf("rules = %+v, parse errors = %+v; want dropped rule and one error", rules, parseErrs)
	}
}
