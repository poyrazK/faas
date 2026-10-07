package main

// adr: 638 — gateway resource policy must fail closed and survive compilation.

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPJWTPolicyLoaderFailureAndCompile(t *testing.T) {
	g := newGatewaydEdgeRules(&fakeEdgeRuleStore{err: errors.New("unavailable")}, newQuietLogger(), nil, nil)
	if rule := g.MatchJWT(context.Background(), "app.example", "/mcp", "POST"); rule == nil || !rule.Unavailable {
		t.Fatal("policy load failure became an authentication bypass")
	}
	p := &api.MCPResourcePolicy{Resource: "https://app.example/mcp", ToolScopes: map[string][]string{}}
	rules, errs := compileJWTRules([]state.EdgeRule{{ID: "mcp", Enabled: true, Kind: state.EdgeRuleKindJWT, MatchPath: "/**", Action: state.EdgeRuleAction{JWT: &state.EdgeRuleJWTAction{Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks", Algorithms: []string{"RS256"}, Audience: []string{p.Resource}, MCP: p}}}})
	if len(errs) != 0 || len(rules) != 1 || rules[0].MCP == nil || rules[0].MCP.ToolScopes == nil {
		t.Fatalf("MCP policy lost during compilation: %+v errors=%v", rules, errs)
	}
}
