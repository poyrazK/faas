package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
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

// A parameter-only rule (OpenAPI apply generates one for every operation
// with typed path parameters) was stored as "schema": null and read back as
// the literal null. The gateway compiled null as a body schema, failed the
// rule, and failed the owner's whole host closed: every request answered
// 503 traffic_policy_unavailable.
func TestCompileValidateRulesStoredParameterOnlyRule(t *testing.T) {
	params := &api.EdgeRuleValidateParameters{
		PathTemplate: "/items/{id}",
		Path:         json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"]}`),
	}
	if stored, err := json.Marshal(state.EdgeRuleValidateAction{Parameters: params, ValidateMode: api.ValidateModeObserve}); err != nil || strings.Contains(string(stored), `"schema"`) {
		t.Fatalf("parameter-only action stored as %s (err %v), want no schema key", stored, err)
	}
	var legacy state.EdgeRuleValidateAction
	if err := json.Unmarshal([]byte(`{"schema": null, "parameters": {"path": {"type": "object", "required": ["id"], "properties": {"id": {"type": "integer"}}}, "path_template": "/items/{id}"}, "validate_mode": "observe"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	g := &gatewaydEdgeRules{validate: newEdgeValidateAdapter(slog.New(slog.NewTextHandler(io.Discard, nil)))}
	out, errs := g.compileValidateRules([]state.EdgeRule{{
		ID: "openapi-items", AccountID: "acct-1", AppID: "app-1", Enabled: true, MatchPath: "/items/?*",
		Kind: state.EdgeRuleKindValidate, Action: state.EdgeRuleAction{Validate: &legacy},
	}})
	if len(errs) != 0 || len(out) != 1 || !out[0].NoBodySchema || out[0].Parameters == nil || !out[0].Parameters.Path.Set {
		t.Fatalf("stored parameter-only rule compiled to %+v with errors %v, want a parameter-only rule", out, errs)
	}
}
