package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func sampleRespondRule(id string, priority int, path string, status int, body []byte) state.EdgeRule {
	return state.EdgeRule{
		ID:        id,
		AccountID: "acct-test",
		AppID:     "app-test",
		MatchHost: "preview.apps.dom",
		MatchPath: path,
		Priority:  priority,
		Enabled:   true,
		Kind:      state.EdgeRuleKindRespond,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindRespond,
			Respond: &state.EdgeRuleRespondAction{
				StatusCode: status,
				Body:       body,
			},
		},
	}
}

func TestCompileRespondRules_FiltersSortsAndCopies(t *testing.T) {
	body := []byte(`{"days":3}`)
	in := []state.EdgeRule{
		sampleRespondRule("high", 20, "/shipping/*", 500, []byte(`{"error":"late"}`)),
		sampleRespondRule("low", 10, "/shipping/*", 200, body),
		{ID: "disabled", Enabled: false, Kind: state.EdgeRuleKindRespond, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRespond, Respond: &state.EdgeRuleRespondAction{StatusCode: 200}}},
		{ID: "wrong-kind", Enabled: true, Kind: state.EdgeRuleKindRoute, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute}},
		sampleRespondRule("bad-status", 1, "/bad", 199, nil),
		sampleRespondRule("bad-json", 2, "/bad", 200, []byte(`{`)),
		sampleRespondRule("bad-204", 3, "/bad", 204, []byte(`{}`)),
		sampleRespondRule("bad-glob", 4, "/shipping/[", 200, nil),
	}

	got, parseErrs := compileRespondRules(in)
	if len(parseErrs) != 1 || parseErrs[0].RuleID != "bad-glob" {
		t.Fatalf("parseErrs = %+v, want only bad-glob", parseErrs)
	}
	if len(got) != 2 {
		t.Fatalf("compiled rules = %d, want 2", len(got))
	}
	if got[0].ID != "low" || got[1].ID != "high" {
		t.Fatalf("compiled order = [%s, %s], want [low, high]", got[0].ID, got[1].ID)
	}
	if got[0].StatusCode != 200 || string(got[0].Body) != string(body) {
		t.Fatalf("compiled low rule = %+v, want status/body from source", got[0])
	}

	// The compiled cache must own its body bytes. A state-store buffer may
	// be reused after loading and must not mutate a response already cached.
	body[0] = 'X'
	if got[0].Body[0] != '{' {
		t.Fatalf("compiled body aliases state buffer: %q", got[0].Body)
	}
}

func TestCompileRespondRules_EmptyInput(t *testing.T) {
	got, parseErrs := compileRespondRules(nil)
	if got != nil || parseErrs != nil {
		t.Fatalf("compileRespondRules(nil) = %v, %v; want nil, nil", got, parseErrs)
	}
}
