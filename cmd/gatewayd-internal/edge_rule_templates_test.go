package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 967 — templated header and redirect actions compile at host load;
// a stored template that no longer compiles drops only its rule.
func TestCompileEdgeRuleTemplates(t *testing.T) {
	headers := []state.EdgeRule{
		{ID: "ok", Enabled: true, Kind: state.EdgeRuleKindHeaders, MatchPath: "/*", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders,
			Headers: &state.EdgeRuleHeadersAction{RequestHeaders: []state.EdgeRuleHeaderOp{{Name: "X-C", Action: "set", Value: "${country}", Template: true}}}}},
		{ID: "bad", Enabled: true, Kind: state.EdgeRuleKindHeaders, MatchPath: "/*", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders,
			Headers: &state.EdgeRuleHeadersAction{ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-C", Action: "set", Value: "${nope}", Template: true}}}}},
	}
	compiled, errs := compileHeadersRules(headers)
	if len(compiled) != 1 || compiled[0].ID != "ok" || compiled[0].RequestHeaders[0].Template == nil || len(errs) != 1 || errs[0].RuleID != "bad" {
		t.Fatalf("headers compiled=%+v errs=%+v", compiled, errs)
	}

	redirects := []state.EdgeRule{
		{ID: "ok", Enabled: true, Kind: state.EdgeRuleKindRedirect, MatchPath: "/*", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect,
			Redirect: &state.EdgeRuleRedirectAction{StatusCode: 308, To: "https://new.example${path}", Template: true, Headers: map[string]string{"X-F": "${host}"}}}},
		{ID: "literal", Enabled: true, Kind: state.EdgeRuleKindRedirect, MatchPath: "/*", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect,
			Redirect: &state.EdgeRuleRedirectAction{StatusCode: 302, To: "/a?x=${raw}"}}},
		{ID: "bad", Enabled: true, Kind: state.EdgeRuleKindRedirect, MatchPath: "/*", Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect,
			Redirect: &state.EdgeRuleRedirectAction{StatusCode: 302, To: "https://${header:x}/a", Template: true}}},
	}
	got, errs := compileRedirectRules(redirects)
	byID := map[string]bool{}
	for _, r := range got {
		byID[r.ID] = true
		if r.ID == "ok" && (r.ToTemplate == nil || r.HeaderTemplates["X-F"] == nil) {
			t.Fatalf("templated redirect not compiled: %+v", r)
		}
		if r.ID == "literal" && r.ToTemplate != nil {
			t.Fatal("untemplated redirect got a template")
		}
	}
	if !byID["ok"] || !byID["literal"] || byID["bad"] || len(errs) != 1 {
		t.Fatalf("redirects=%v errs=%+v", byID, errs)
	}
}
