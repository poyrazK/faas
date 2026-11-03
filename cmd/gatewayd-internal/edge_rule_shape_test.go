package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — every kind in state.EdgeRuleKind has a gateway shape, so a kind
// added later cannot reach this gateway's compilers unrecognised (amendment
// 2026-11-02, review round 8).
func TestEveryEdgeRuleKindHasAGatewayShape(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "../../pkg/state/types.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []state.EdgeRuleKind
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || spec.Type == nil {
			return true
		}
		if ident, ok := spec.Type.(*ast.Ident); !ok || ident.Name != "EdgeRuleKind" {
			return true
		}
		for _, value := range spec.Values {
			if lit, ok := value.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				kind, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				kinds = append(kinds, state.EdgeRuleKind(kind))
			}
		}
		return true
	})
	if len(kinds) < 20 {
		t.Fatalf("found %d edge rule kinds in pkg/state/types.go, want at least 20", len(kinds))
	}
	for _, kind := range kinds {
		if _, known := edgeRulePayloadPresent(state.EdgeRule{Kind: kind}); !known {
			t.Errorf("edge rule kind %q has no case in edgeRulePayloadPresent", kind)
		}
	}
}

// adr: 091 — an enabled rule of a kind the gateway does not know, or one whose
// payload is missing, fails its host closed instead of being skipped.
func TestEdgeRuleShapeErrorRefusesUnknownKindsAndMissingPayloads(t *testing.T) {
	ip := state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"203.0.113.0/24"}}}
	cases := []struct {
		name    string
		rule    state.EdgeRule
		wantErr bool
	}{
		{"complete", state.EdgeRule{ID: "a", Enabled: true, Kind: state.EdgeRuleKindIP, Action: ip}, false},
		{"missing payload", state.EdgeRule{ID: "b", Enabled: true, Kind: state.EdgeRuleKindIP}, true},
		{"unknown kind", state.EdgeRule{ID: "c", Enabled: true, Kind: "bot_score"}, true},
		{"disabled unknown kind", state.EdgeRule{ID: "d", Kind: "bot_score"}, false},
	}
	for _, tc := range cases {
		if err := edgeRuleShapeError(tc.rule); (err != nil) != tc.wantErr {
			t.Errorf("%s: err = %v, want error %v", tc.name, err, tc.wantErr)
		}
	}
}
