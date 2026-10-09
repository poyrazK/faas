package main

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func wafStateRule(id string, priority int, path string, action *state.EdgeRuleWAFAction) state.EdgeRule {
	return state.EdgeRule{
		ID: id, AccountID: "acct_1", AppID: "app_1",
		MatchPath: path, Priority: priority, Enabled: true,
		Kind:   state.EdgeRuleKindWAF,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF, WAF: action},
	}
}

func TestCompileWAFRules(t *testing.T) {
	valid := &state.EdgeRuleWAFAction{Mode: "observe", ParanoiaLevel: 2, AnomalyThreshold: 8, ExcludeRuleIDs: []int{942100}}
	disabled := wafStateRule("disabled", 0, "/*", valid)
	disabled.Enabled = false
	otherKind := wafStateRule("other", 1, "/*", valid)
	otherKind.Kind = state.EdgeRuleKindRoute
	rules, parseErrs := compileWAFRules([]state.EdgeRule{
		disabled,
		otherKind,
		wafStateRule("missing-action", 2, "/*", nil),
		wafStateRule("block-mode", 3, "/*", &state.EdgeRuleWAFAction{Mode: "block"}),
		wafStateRule("bad-glob", 4, "[unclosed", valid),
		wafStateRule("second", 20, "/*", &state.EdgeRuleWAFAction{}),
		wafStateRule("first", 10, "/api/*", valid),
	})
	if len(parseErrs) != 1 {
		t.Errorf("parse errors = %v, want one for bad-glob", parseErrs)
	}
	if len(rules) != 2 || rules[0].ID != "first" || rules[1].ID != "second" {
		t.Fatalf("compiled = %+v, want [first second]", rules)
	}
	first := rules[0]
	if first.ParanoiaLevel != 2 || first.AnomalyThreshold != 8 || !slices.Equal(first.ExcludeRuleIDs, []int{942100}) {
		t.Errorf("first = %+v, want stored scoring values", first)
	}
	// A row written without effective values compiles with the defaults
	// apid would have stored.
	if rules[1].ParanoiaLevel != 1 || rules[1].AnomalyThreshold != 5 {
		t.Errorf("second = %+v, want defaults PL1 / threshold 5", rules[1])
	}
}
