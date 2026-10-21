package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func challengeStateRule(id string, priority int, path string, action *state.EdgeRuleChallengeAction) state.EdgeRule {
	return state.EdgeRule{
		ID: id, AccountID: "acct_1", AppID: "app_1", MatchHost: "shop.example.com",
		MatchPath: path, Priority: priority, Enabled: true,
		Kind:   state.EdgeRuleKindChallenge,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindChallenge, Challenge: action},
	}
}

func TestCompileChallengeRules(t *testing.T) {
	valid := &state.EdgeRuleChallengeAction{Difficulty: 18, PassTTLSeconds: 900}
	disabled := challengeStateRule("disabled", 0, "/*", valid)
	disabled.Enabled = false
	otherKind := challengeStateRule("other", 1, "/*", valid)
	otherKind.Kind = state.EdgeRuleKindRoute
	rules, parseErrs := compileChallengeRules([]state.EdgeRule{
		disabled,
		otherKind,
		challengeStateRule("missing-action", 2, "/*", nil),
		challengeStateRule("too-easy", 3, "/*", &state.EdgeRuleChallengeAction{Difficulty: 4}),
		challengeStateRule("bad-glob", 4, "[unclosed", valid),
		challengeStateRule("second", 20, "/*", &state.EdgeRuleChallengeAction{}),
		challengeStateRule("first", 10, "/account/*", valid),
	})
	if len(parseErrs) != 1 {
		t.Errorf("parse errors = %v, want one for bad-glob", parseErrs)
	}
	if len(rules) != 2 || rules[0].ID != "first" || rules[1].ID != "second" {
		t.Fatalf("compiled = %+v, want [first second]", rules)
	}
	if rules[0].Difficulty != 18 || rules[0].PassTTLSeconds != 900 {
		t.Errorf("first = %+v, want the stored values", rules[0])
	}
	// A row written without effective values compiles with apid's defaults.
	if rules[1].Difficulty != 16 || rules[1].PassTTLSeconds != 3600 {
		t.Errorf("second = %+v, want difficulty 16 and a one-hour pass", rules[1])
	}
}

func TestMatchChallenge(t *testing.T) {
	const host = "shop.example.com"
	logged := challengeStateRule("logged", 1, "/*", &state.EdgeRuleChallengeAction{})
	logged.Mode = "log"
	store := &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{host: {
		logged,
		challengeStateRule("account", 10, "/account/*", &state.EdgeRuleChallengeAction{}),
	}}}
	matcher := newGatewaydEdgeRules(store, newQuietLogger(), nil, nil)
	ctx := context.Background()
	if rule := matcher.MatchChallenge(ctx, host, "/account/orders", "GET"); rule == nil || rule.ID != "account" {
		t.Fatalf("MatchChallenge(/account/orders) = %+v, want the enforced account rule", rule)
	}
	if rule := matcher.MatchChallenge(ctx, host, "/blog", "GET"); rule != nil {
		t.Fatalf("MatchChallenge(/blog) = %+v, want none: only a log-mode rule matches", rule)
	}
	if rule := matcher.HostChallengeRule(ctx, host); rule == nil || rule.ID != "account" {
		t.Fatalf("HostChallengeRule = %+v, want the enforced rule", rule)
	}

	onlyLogged := &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{host: {logged}}}
	if rule := newGatewaydEdgeRules(onlyLogged, newQuietLogger(), nil, nil).HostChallengeRule(ctx, host); rule != nil {
		t.Fatalf("HostChallengeRule with only a log-mode rule = %+v, want none", rule)
	}
}
