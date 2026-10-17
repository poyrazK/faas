package main

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPinnedHostPolicySurvivesRefreshAndStoreFailure(t *testing.T) {
	const host = "app.example.test"
	store := &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{host: {
		sampleRouteRule("route-old", 1, host, "/*", nil, "old"),
	}}}
	g := newGatewaydEdgeRules(store, nil, nil, nil)
	ctx, err := g.PinHostPolicy(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := gateway.PinnedHostPolicy(ctx, host)
	before := gateway.PinnedHostPolicyRevision(ctx, host)
	store.mu.Lock()
	store.rules[host] = []state.EdgeRule{sampleRouteRule("route-new", 1, host, "/*", nil, "new")}
	store.mu.Unlock()
	g.Reset()
	if route := g.MatchRoute(ctx, host, "/wake", "GET"); route == nil || route.TargetAppSlug != "old" {
		t.Fatalf("wake changed admitted route: %+v", route)
	}
	if route := g.MatchRoute(t.Context(), host, "/wake", "GET"); route == nil || route.TargetAppSlug != "new" {
		t.Fatalf("new request missed refreshed route: %+v", route)
	}
	store.mu.Lock()
	store.err = errors.New("store unavailable")
	store.mu.Unlock()
	g.Reset()
	if route := g.MatchRoute(ctx, host, "/retry", "GET"); route == nil || route.TargetAppSlug != "old" {
		t.Fatalf("store failure changed admitted policy: %+v", route)
	}
	if _, err := g.PinHostPolicy(t.Context(), host); err == nil {
		t.Fatal("new unverified policy was admitted")
	}
	if entry.Host != host || before == "" {
		t.Fatalf("snapshot identity missing: %+v %s", entry, before)
	}
}

func TestPinnedHostPolicyRefusesInvalidationBetweenSelectorHostsAndCompileErrors(t *testing.T) {
	store := &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{}}
	g := newGatewaydEdgeRules(store, nil, nil, nil)
	ctx, err := g.PinHostPolicy(t.Context(), "app--port-http.example.test")
	if err != nil {
		t.Fatal(err)
	}
	g.Reset()
	if _, err := g.PinHostPolicy(ctx, "app.example.test"); err == nil {
		t.Fatal("two host policies from different refreshes were combined")
	}
	store.rules["broken.test"] = []state.EdgeRule{sampleRouteRule("invalid", 1, "broken.test", "[", nil, "app")}
	broken, err := g.PinHostPolicy(t.Context(), "broken.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := gateway.ValidatePinnedHostPolicies(broken, "acc_test"); err == nil {
		t.Fatal("owner compile failure admitted an empty protection policy")
	}
	if err := gateway.ValidatePinnedHostPolicies(broken, "another-owner"); err != nil {
		t.Fatalf("foreign broken policy blocked another tenant: %v", err)
	}
}

func TestPinnedHostPolicyIncludesResolvedPresetAndPinsRetryAndBudget(t *testing.T) {
	const host, presetID = "app.example.test", "preset"
	preset := state.CorsPreset{ID: presetID, AccountID: "owner", AllowOrigins: []string{"https://old.test"}, AllowMethods: []string{"GET"}}
	retry := retryRule("retry", &state.EdgeRuleRetryAction{MaxAttempts: 2})
	retry.AccountID = "owner"
	budget := state.EdgeRule{ID: "budget", AccountID: "owner", Enabled: true, Kind: state.EdgeRuleKindBudget,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindBudget, Budget: &state.EdgeRuleBudgetAction{BudgetMs: 1000, TotalDeadlineMs: 2000}}}
	store := &fakeEdgeRuleStore{rules: map[string][]state.EdgeRule{host: {
		sampleCorsRule("cors", "owner", "app", host, "", ptrString(presetID), nil, nil), retry, budget,
	}}, presetBy: map[string]state.CorsPreset{"owner:" + presetID: preset}}
	g := newGatewaydEdgeRules(store, nil, nil, nil)
	ctx, err := g.PinHostPolicy(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	before := gateway.PinnedHostPolicyRevision(ctx, host)
	preset.AllowOrigins = []string{"https://new.test"}
	store.mu.Lock()
	store.presetBy["owner:"+presetID] = preset // same rule JSON, different resolved input
	store.rules[host][1].Action.Retry = &state.EdgeRuleRetryAction{MaxAttempts: 4}
	store.rules[host][2].Action.Budget = &state.EdgeRuleBudgetAction{BudgetMs: 3000, TotalDeadlineMs: 4000}
	store.mu.Unlock()
	g.Reset()
	after, err := g.PinHostPolicy(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	if gateway.PinnedHostPolicyRevision(after, host) == before {
		t.Fatal("resolved preset and action changes did not change effective digest")
	}
	if rule := g.MatchCORS(ctx, host, "/", "GET"); rule == nil || rule.AllowOrigins[0] != "https://old.test" {
		t.Fatalf("admitted request changed CORS preset: %+v", rule)
	}
	if rule := g.MatchRetry(ctx, host, "/", "GET"); rule == nil || rule.MaxAttempts != 2 {
		t.Fatalf("admitted request changed replay policy: %+v", rule)
	}
	if rule := g.MatchBudget(ctx, host, "/", "GET"); rule == nil || rule.TotalDeadlineMs != 2000 {
		t.Fatalf("admitted request changed deadline: %+v", rule)
	}
}
