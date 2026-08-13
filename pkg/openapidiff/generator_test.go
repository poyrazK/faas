package openapidiff

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestGenerateFromEdgeRules_AddsRoutePath — a manifest with
// kind="route" projects a new path entry onto the embedded
// spec. The differ then sees baseline (no path) vs proposed
// (path present) and emits zero breaks (path adds are
// Diff.Changes, not Breaks). This test pins the generator's
// projection shape.
func TestGenerateFromEdgeRules_AddsRoutePath(t *testing.T) {
	embedded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	pending := []api.CreateEdgeRuleRequest{
		{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo"},
	}
	// Seed baseline with no previous routes.
	out, err := GenerateFromEdgeRules(embedded, nil, pending)
	if err != nil {
		t.Fatalf("GenerateFromEdgeRules: %v", err)
	}
	pi, ok := out.Paths["api.example.com/v1/foo"]
	if !ok {
		t.Fatalf("expected api.example.com/v1/foo to be projected; got %d paths", len(out.Paths))
	}
	if _, ok := pi.Methods["get"]; !ok {
		t.Fatalf("expected default GET method; got %v", pi.Methods)
	}
}

// TestGenerateFromEdgeRules_RemovedPath_FiresBreak — a route
// edge rule present in the baseline rules but absent from the
// pending rules must produce a SchemaBreak with Kind=FieldRemoved
// when the engine compares baseline-projected vs proposed-projected
// specs. This is the v1 high-precision structural signal: a
// removed route path is a customer-visible wire change.
func TestGenerateFromEdgeRules_RemovedPath_FiresBreak(t *testing.T) {
	embedded, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	prevRules := []api.CreateEdgeRuleRequest{
		{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo"},
	}
	// Baseline spec reflects the previous deploy.
	bSpec, err := GenerateFromEdgeRules(embedded, prevRules, prevRules)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Proposed spec reflects the new deploy (route removed).
	pSpec, err := GenerateFromEdgeRules(embedded, prevRules, nil)
	if err != nil {
		t.Fatalf("proposed: %v", err)
	}
	breaks := Compare(bSpec, pSpec)
	if len(breaks) != 1 {
		t.Fatalf("expected 1 break; got %d: %+v", len(breaks), breaks)
	}
	if breaks[0].Kind != SchemaKindFieldRemoved {
		t.Fatalf("expected SchemaKindFieldRemoved; got %q", breaks[0].Kind)
	}
	if breaks[0].Path != "api.example.com/v1/foo" {
		t.Fatalf("expected path=api.example.com/v1/foo; got %q", breaks[0].Path)
	}
}

// TestGenerateFromEdgeRules_IdenticalDeployIsZero — when the
// baseline and pending rule lists are identical, the differ
// must produce zero breaks. Pins the no-op deploy silence.
func TestGenerateFromEdgeRules_IdenticalDeployIsZero(t *testing.T) {
	embedded, _ := Load()
	rules := []api.CreateEdgeRuleRequest{
		{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo"},
		{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/bar", MatchMethods: []string{"POST"}},
	}
	bSpec, _ := GenerateFromEdgeRules(embedded, rules, rules)
	pSpec, _ := GenerateFromEdgeRules(embedded, rules, rules)
	breaks := Compare(bSpec, pSpec)
	if len(breaks) != 0 {
		t.Fatalf("identical rule lists must produce 0 breaks; got %d: %+v", len(breaks), breaks)
	}
}

// TestGenerateFromEdgeRules_OtherKindsIgnored — only kind="route"
// edge rules contribute OpenAPI path entries. Other kinds
// (rewrite / redirect / headers / cors / jwt / ip) modify
// existing path responses; PR-2's differ does not project
// them, and the engine surfaces their changes via the
// existing edge-rule walker.
func TestGenerateFromEdgeRules_OtherKindsIgnored(t *testing.T) {
	embedded, _ := Load()
	pending := []api.CreateEdgeRuleRequest{
		{Kind: "rewrite", MatchHost: "api.example.com", MatchPath: "/v1/foo"},
		{Kind: "redirect", MatchHost: "api.example.com", MatchPath: "/v1/bar"},
	}
	out, err := GenerateFromEdgeRules(embedded, nil, pending)
	if err != nil {
		t.Fatalf("GenerateFromEdgeRules: %v", err)
	}
	for _, pathKey := range []string{"api.example.com/v1/foo", "api.example.com/v1/bar"} {
		if _, ok := out.Paths[pathKey]; ok {
			t.Fatalf("non-route kind projected a path entry: %q", pathKey)
		}
	}
}

// TestGenerateFromEdgeRules_MultipleMethodsOnOnePath — two
// route rules for the same (host, path) with different methods
// must produce ONE OpenAPI path entry with the union of methods.
func TestGenerateFromEdgeRules_MultipleMethodsOnOnePath(t *testing.T) {
	embedded, _ := Load()
	pending := []api.CreateEdgeRuleRequest{
		{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo", MatchMethods: []string{"GET"}},
		{Kind: "route", MatchHost: "api.example.com", MatchPath: "/v1/foo", MatchMethods: []string{"POST"}},
	}
	out, _ := GenerateFromEdgeRules(embedded, nil, pending)
	pi, ok := out.Paths["api.example.com/v1/foo"]
	if !ok {
		t.Fatalf("expected api.example.com/v1/foo in projected paths")
	}
	if _, ok := pi.Methods["get"]; !ok {
		t.Fatalf("expected GET method")
	}
	if _, ok := pi.Methods["post"]; !ok {
		t.Fatalf("expected POST method")
	}
}
