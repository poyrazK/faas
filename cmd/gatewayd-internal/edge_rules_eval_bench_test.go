package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// BenchmarkEdgeRuleEvaluation500Rules measures what one request costs the
// edge-rule matcher when the host carries the per-app maximum of 500 rules
// (Scale), with the compiled set cached (ADR-091 amendment 2026-10-27, GA
// hardening). The rules spread over the kinds a request consults in order,
// each on its own path, and the request matches only the last rule of each
// kind, so every kind scans its whole slice: the worst case for a host. The
// result is recorded in docs/adr/091-edge-rules.md.
func BenchmarkEdgeRuleEvaluation500Rules(b *testing.B) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "bench@example.test", api.PlanScale)
	app, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "bench", Status: state.AppActive})
	actions := []state.EdgeRuleAction{
		{Kind: state.EdgeRuleKindMaintenance, Maintenance: &state.EdgeRuleMaintenanceAction{Message: "down"}},
		{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: 308, To: "/moved"}},
		{Kind: state.EdgeRuleKindRewrite, Rewrite: &state.EdgeRuleRewriteAction{From: "/svc", To: "/v2"}},
		{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Bench", Value: "1", Action: "set"}}}},
		{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"192.0.2.0/24"}}},
		{Kind: state.EdgeRuleKindGeo, Geo: &state.EdgeRuleGeoAction{Deny: []string{"KP"}}},
		{Kind: state.EdgeRuleKindLimit, Limit: &state.EdgeRuleLimitAction{MaxBodyBytes: 1 << 20}},
		{Kind: state.EdgeRuleKindThrottle, Throttle: &state.EdgeRuleThrottleAction{RequestsPerSecond: 10, Burst: 20}},
		{Kind: state.EdgeRuleKindBudget, Budget: &state.EdgeRuleBudgetAction{BudgetMs: 5000}},
		{Kind: state.EdgeRuleKindCache, Cache: &state.EdgeRuleCacheAction{MaxAgeSeconds: 60}},
	}
	const rules = 500
	perKind := rules / len(actions)
	for i := range rules {
		action := actions[i%len(actions)]
		if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID,
			MatchHost: "bench.example.com", MatchPath: fmt.Sprintf("/svc/%d/**", i/len(actions)),
			Priority: i, Enabled: true, Kind: action.Kind, Action: action}); err != nil {
			b.Fatal(err)
		}
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	reqCtx := gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, acct.ID), app.ID)
	host, path, method := "bench.example.com", fmt.Sprintf("/svc/%d/items", perKind-1), "GET"
	match := func() int {
		hits := 0
		for _, found := range []bool{
			matcher.MatchMaintenance(reqCtx, host, path, method) != nil,
			matcher.MatchRedirect(reqCtx, host, path, method) != nil,
			matcher.MatchRewrite(reqCtx, host, path, method) != nil,
			matcher.MatchHeaders(reqCtx, host, path, method) != nil,
			matcher.MatchCORS(reqCtx, host, path, method) != nil,
			matcher.MatchIP(reqCtx, host, path, method) != nil,
			matcher.MatchGeo(reqCtx, host, path, method) != nil,
			matcher.MatchJWT(reqCtx, host, path, method) != nil,
			matcher.MatchLimit(reqCtx, host, path, method) != nil,
			matcher.MatchThrottle(reqCtx, host, path, method) != nil,
			matcher.MatchWAF(reqCtx, host, path, method) != nil,
			matcher.MatchValidate(reqCtx, host, path, method) != nil,
			matcher.MatchChallenge(reqCtx, host, path, method) != nil,
			matcher.MatchRespond(reqCtx, host, path, method) != nil,
			matcher.MatchCache(reqCtx, host, path, method) != nil,
			matcher.MatchAsync(reqCtx, host, path, method) != nil,
			matcher.MatchBudget(reqCtx, host, path, method) != nil,
		} {
			if found {
				hits++
			}
		}
		return hits
	}
	if hits := match(); hits != len(actions) {
		b.Fatalf("the request matched %d kinds, want %d (one rule of each seeded kind)", hits, len(actions))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		match()
	}
}
