package edgerules

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// releaseHost decides platform flags from a fixed set.
type releaseHost struct {
	suggestionHost
	released map[string]bool
}

func (h releaseHost) PlatformFlagEnabled(flag string, _ state.Account) bool { return h.released[flag] }

// adr: 20261023113721 — an update is refused only when it switches on a
// gated feature the rule does not already enforce: enabling a rule, leaving
// log mode, moving WAF to warn or block, or adding a penalty.
func TestUpdateReleaseProblem(t *testing.T) {
	svc := &Service{Host: releaseHost{}}
	on, off := true, false
	enforce, logMode := api.EdgeRuleModeEnforce, api.EdgeRuleModeLog
	block := json.RawMessage(`{"mode":"block"}`)
	observe := json.RawMessage(`{"mode":"observe"}`)
	name := "renamed"
	challenge := func(enabled bool, mode string) state.EdgeRule {
		return state.EdgeRule{Kind: state.EdgeRuleKindChallenge, Enabled: enabled, Mode: mode,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindChallenge}}
	}
	waf := func(enabled bool, mode string) state.EdgeRule {
		return state.EdgeRule{Kind: state.EdgeRuleKindWAF, Enabled: enabled,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF, WAF: &state.EdgeRuleWAFAction{Mode: mode}}}
	}
	penalized := state.EdgeRule{Kind: state.EdgeRuleKindIP, Enabled: true,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, Penalty: &state.EdgeRulePenalty{Seconds: 600}}}
	for _, tc := range []struct {
		name    string
		row     state.EdgeRule
		req     api.UpdateEdgeRuleRequest
		refused bool
	}{
		{"enable a disabled challenge rule", challenge(false, ""), api.UpdateEdgeRuleRequest{Enabled: &on}, true},
		{"take a challenge rule out of log mode", challenge(true, logMode), api.UpdateEdgeRuleRequest{Mode: &enforce}, true},
		{"edit an enforced challenge rule", challenge(true, ""), api.UpdateEdgeRuleRequest{Name: &name}, false},
		{"put a challenge rule in log mode", challenge(true, ""), api.UpdateEdgeRuleRequest{Mode: &logMode}, false},
		{"edit a disabled challenge rule", challenge(false, ""), api.UpdateEdgeRuleRequest{Name: &name}, false},
		{"move WAF from observe to block", waf(true, ""), api.UpdateEdgeRuleRequest{Action: &block}, true},
		{"enable a disabled block-mode WAF rule", waf(false, api.EdgeWAFModeBlock), api.UpdateEdgeRuleRequest{Enabled: &on}, true},
		{"enable a disabled observe WAF rule", waf(false, ""), api.UpdateEdgeRuleRequest{Enabled: &on}, false},
		{"keep an enforced block-mode WAF rule", waf(true, api.EdgeWAFModeBlock), api.UpdateEdgeRuleRequest{Action: &block}, false},
		{"move WAF back to observe", waf(true, api.EdgeWAFModeBlock), api.UpdateEdgeRuleRequest{Action: &observe}, false},
		{"change an enforced penalty", penalized, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 900}}, false},
		{"disable a penalized rule", penalized, api.UpdateEdgeRuleRequest{Enabled: &off}, false},
		{"add a penalty", state.EdgeRule{Kind: state.EdgeRuleKindIP, Enabled: true}, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 600}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prob := svc.updateReleaseProblem(state.Account{ID: "acct"}, tc.row, tc.req)
			if (prob != nil) != tc.refused {
				t.Fatalf("problem = %v, refused want %v", prob, tc.refused)
			}
			if prob != nil && (prob.Code != api.CodeEdgeFeatureUnavailable || prob.Status != 422) {
				t.Fatalf("problem = %+v", prob)
			}
		})
	}
	released := &Service{Host: releaseHost{released: map[string]bool{platformflags.EdgeChallenge: true}}}
	if prob := released.updateReleaseProblem(state.Account{ID: "acct"}, challenge(false, ""), api.UpdateEdgeRuleRequest{Enabled: &on}); prob != nil {
		t.Fatalf("released flag refused: %v", prob)
	}
}

// adr: 20261023113721 — creating a rule that uses a gated feature is refused
// whether or not it starts enabled; each feature answers to its own flag.
func TestCreateReleaseProblem(t *testing.T) {
	off := false
	challenge := api.CreateEdgeRuleRequest{Kind: "challenge", Action: json.RawMessage(`{}`), Enabled: &off}
	wafWarn := api.CreateEdgeRuleRequest{Kind: "waf", Action: json.RawMessage(`{"mode":"warn"}`)}
	wafObserve := api.CreateEdgeRuleRequest{Kind: "waf", Action: json.RawMessage(`{}`)}
	acct := state.Account{ID: "acct"}
	none := &Service{Host: releaseHost{}}
	for name, req := range map[string]api.CreateEdgeRuleRequest{"disabled challenge": challenge, "waf warn": wafWarn} {
		if none.createReleaseProblem(acct, req) == nil {
			t.Fatalf("%s created while unreleased", name)
		}
	}
	if prob := none.createReleaseProblem(acct, wafObserve); prob != nil {
		t.Fatalf("observe WAF refused: %v", prob)
	}
	onlyChallenge := &Service{Host: releaseHost{released: map[string]bool{platformflags.EdgeChallenge: true}}}
	if prob := onlyChallenge.createReleaseProblem(acct, challenge); prob != nil {
		t.Fatalf("released challenge refused: %v", prob)
	}
	if onlyChallenge.createReleaseProblem(acct, wafWarn) == nil {
		t.Fatal("edge_challenge released WAF warn")
	}
}
