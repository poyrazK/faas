package edgerules

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022105846 — create validates a penalty against the rule's kind
// and stores it with its defaults; the response returns it.
func TestCreateValidatesAndStoresPenalty(t *testing.T) {
	ipAction := json.RawMessage(`{"deny":["203.0.113.0/24"]}`)
	req := api.CreateEdgeRuleRequest{MatchHost: "a.example.com", MatchPath: "/", Kind: "ip", Action: ipAction,
		Penalty: &api.EdgeRulePenalty{Seconds: 600}}
	if prob := ValidateEdgeRuleBody(&req, api.PlanPro); prob != nil {
		t.Fatalf("ip penalty refused: %s", prob.Detail)
	}
	action := withPenalty(ActionFromBody(req.Kind, req.Action), req.Penalty)
	if action.Penalty == nil || *action.Penalty != (state.EdgeRulePenalty{Seconds: 600, After: 1, WithinSeconds: 60}) {
		t.Fatalf("stored penalty = %+v, want the defaults applied", action.Penalty)
	}
	resp := EdgeRuleResponse(state.EdgeRule{ID: "r1", Kind: state.EdgeRuleKindIP, Action: action})
	if resp.Penalty == nil || resp.Penalty.Seconds != 600 || resp.Penalty.After != 1 {
		t.Fatalf("response penalty = %+v", resp.Penalty)
	}

	for name, bad := range map[string]api.CreateEdgeRuleRequest{
		"challenge kind": {MatchHost: "a.example.com", MatchPath: "/", Kind: "challenge", Action: json.RawMessage(`{}`),
			Penalty: &api.EdgeRulePenalty{Seconds: 600}},
		"waf observe": {MatchHost: "a.example.com", MatchPath: "/", Kind: "waf", Action: json.RawMessage(`{"mode":"observe"}`),
			Penalty: &api.EdgeRulePenalty{Seconds: 600}},
		"too short": {MatchHost: "a.example.com", MatchPath: "/", Kind: "ip", Action: ipAction,
			Penalty: &api.EdgeRulePenalty{Seconds: 10}},
	} {
		if prob := ValidateEdgeRuleBody(&bad, api.PlanPro); prob == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// adr: 20261022105846 — a PATCH keeps the stored penalty across an action
// replacement, replaces it with penalty, removes it with clear_penalty, and
// refuses a waf rule leaving block mode while it keeps one.
func TestPenaltyUpdateParams(t *testing.T) {
	stored := &state.EdgeRulePenalty{Seconds: 600, After: 2, WithinSeconds: 60}
	row := state.EdgeRule{ID: "r1", Kind: state.EdgeRuleKindIP, Action: state.EdgeRuleAction{
		Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"203.0.113.0/24"}}, Penalty: stored}}
	newAction := json.RawMessage(`{"deny":["198.51.100.0/24"]}`)

	actionOnly := api.UpdateEdgeRuleRequest{Action: &newAction}
	p := penaltyUpdateParams(row, actionOnly, edgeRuleUpdateParamsFrom(actionOnly, row.Kind))
	if p.Action == nil || p.Action.Penalty != stored || p.Action.IP == nil || p.Action.IP.Deny[0] != "198.51.100.0/24" {
		t.Fatalf("action replacement = %+v, want the new action with the stored penalty", p.Action)
	}
	replace := api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 3600, After: 1, WithinSeconds: 60}}
	p = penaltyUpdateParams(row, replace, edgeRuleUpdateParamsFrom(replace, row.Kind))
	if p.Action == nil || p.Action.Penalty.Seconds != 3600 || p.Action.IP == nil {
		t.Fatalf("penalty replacement = %+v", p.Action)
	}
	clearReq := api.UpdateEdgeRuleRequest{ClearPenalty: true}
	if p = penaltyUpdateParams(row, clearReq, edgeRuleUpdateParamsFrom(clearReq, row.Kind)); p.Action == nil || p.Action.Penalty != nil {
		t.Fatalf("clear_penalty = %+v", p.Action)
	}
	if p = penaltyUpdateParams(row, api.UpdateEdgeRuleRequest{}, state.UpdateEdgeRuleParams{}); p.Action != nil {
		t.Fatal("an unrelated PATCH rewrote the action")
	}

	if prob := validatePenaltyUpdate(row, api.UpdateEdgeRuleRequest{Penalty: &api.EdgeRulePenalty{Seconds: 600}, ClearPenalty: true}); prob == nil {
		t.Fatal("penalty with clear_penalty accepted")
	}
	waf := state.EdgeRule{Kind: state.EdgeRuleKindWAF, Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF,
		WAF: &state.EdgeRuleWAFAction{Mode: api.EdgeWAFModeBlock}, Penalty: stored}}
	observe := json.RawMessage(`{"mode":"observe"}`)
	if prob := validatePenaltyUpdate(waf, api.UpdateEdgeRuleRequest{Action: &observe}); prob == nil {
		t.Fatal("waf rule left block mode while keeping its penalty")
	}
	if prob := validatePenaltyUpdate(waf, api.UpdateEdgeRuleRequest{Action: &observe, ClearPenalty: true}); prob != nil {
		t.Fatalf("waf rule leaving block mode and dropping its penalty refused: %s", prob.Detail)
	}
}
