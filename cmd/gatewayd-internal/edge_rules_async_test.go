package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
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
	configured := asyncStateRule("first", 10, "/reports", "post", "PUT")
	configured.Action.Async = &state.EdgeRuleAsyncAction{
		RetryPolicy:   &api.RetryPolicyDTO{MaxAttempts: 4, BaseSeconds: 1, MaxSeconds: 30, JitterSeconds: 0.2},
		MaxAgeSeconds: 600,
	}

	rules, parseErrs := compileAsyncRules([]state.EdgeRule{
		asyncStateRule("later", 20, "/*"),
		disabled,
		missingAction,
		otherKind,
		configured,
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
	if rules[0].RetryPolicy == nil || rules[0].RetryPolicy.MaxAttempts != 4 || rules[0].RetryPolicy.BaseSeconds != 1 || rules[0].MaxAgeSeconds != 600 {
		t.Errorf("compiled execution policy = retry %+v, max_age=%d", rules[0].RetryPolicy, rules[0].MaxAgeSeconds)
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
