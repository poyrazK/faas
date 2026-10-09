package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCompileValidateRulesParameters(t *testing.T) {
	g := &gatewaydEdgeRules{validate: newEdgeValidateAdapter(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	rule := func(id, matchPath string, action state.EdgeRuleValidateAction) state.EdgeRule {
		return state.EdgeRule{
			ID: id, AccountID: "acct-1", AppID: "app-1", Enabled: true, MatchPath: matchPath,
			Kind: state.EdgeRuleKindValidate, Action: state.EdgeRuleAction{Validate: &action},
		}
	}
	params := &api.EdgeRuleValidateParameters{
		PathTemplate: "/users/{id}",
		Path:         json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`),
		Query:        json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer"}}}`),
	}
	out, errs := g.compileValidateRules([]state.EdgeRule{
		rule("params-only", "/users/?*", state.EdgeRuleValidateAction{Parameters: params}),
		rule("body-and-query", "/items", state.EdgeRuleValidateAction{
			Schema:     json.RawMessage(`{"type":"object"}`),
			Parameters: &api.EdgeRuleValidateParameters{Query: params.Query},
		}),
		// A row that bypassed apid with a misaligned path is dropped, not half-applied.
		rule("misaligned", "/users/*", state.EdgeRuleValidateAction{Parameters: params}),
		rule("empty", "/x", state.EdgeRuleValidateAction{}),
	})
	if len(out) != 2 || len(errs) != 2 {
		t.Fatalf("compiled %d rules with %d errors (%v), want 2 and 2", len(out), len(errs), errs)
	}
	byID := map[string]int{}
	for i, r := range out {
		byID[r.ID] = i
	}
	only := out[byID["params-only"]]
	if !only.NoBodySchema || only.Parameters == nil || !only.Parameters.Path.Set || !only.Parameters.Query.Set ||
		only.Parameters.Headers.Set || only.Parameters.PathTemplate != "/users/{id}" ||
		only.Parameters.Path.Kinds["id"] != (api.EdgeRuleParamKind{Type: "integer"}) {
		t.Fatalf("params-only = %+v / %+v", only, only.Parameters)
	}
	both := out[byID["body-and-query"]]
	if both.NoBodySchema || both.SchemaDigest == ([32]byte{}) || both.Parameters == nil || !both.Parameters.Query.Set {
		t.Fatalf("body-and-query = %+v", both)
	}
}
