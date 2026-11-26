package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261121170000 — a route split relies on how the gateway scopes rules
// for a routed request. The request keeps the source's host and is served as
// the route's app, so:
//   - the source's host rules still match it (same edge-rule scope);
//   - the deny-all block on the route's app's own hosts never does;
//   - the source's app-wide ("@app") rules do not, which is why a split
//     refuses an app that has them.
func TestRouteSplitRuleScope(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	shop, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	exports, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop-exports", Status: state.AppActive})
	deny := func(cidrs ...string) state.EdgeRuleAction {
		return state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: cidrs}}
	}
	rule := func(app state.App, host, path string) state.EdgeRule {
		t.Helper()
		created, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID,
			MatchHost: host, MatchPath: path, Priority: 10, Enabled: true, Kind: state.EdgeRuleKindIP, Action: deny("0.0.0.0/0", "::/0")})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	sourceHost := rule(shop, "shop.gregale.dev", "/admin/**")
	sourceAppWide := rule(shop, api.EdgeRuleAppHosts, "/internal/**")
	block := rule(exports, "shop-exports.gregale.dev", "/**")
	selectorBlock := rule(exports, "shop-exports--*.gregale.dev", "/**")

	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	servedAs := func(app state.App) context.Context {
		return gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, acct.ID), app.ID)
	}
	matchID := func(app state.App, host, path string) string {
		if got := matcher.MatchIP(servedAs(app), host, path, "GET"); got != nil {
			return got.ID
		}
		return ""
	}

	// A routed request: the source's host, served as the route's app.
	if got := matchID(exports, "shop.gregale.dev", "/admin/users"); got != sourceHost.ID {
		t.Fatalf("routed request to /admin = %q, want the source's host rule", got)
	}
	if got := matchID(exports, "shop.gregale.dev", "/exports/1"); got != "" {
		t.Fatalf("routed request to /exports = %q, want no block", got)
	}
	if got := matchID(exports, "shop.gregale.dev", "/internal/x"); got == sourceAppWide.ID {
		t.Fatal("the source's app-wide rule ran on a routed request; splits may stop refusing @app rules")
	}
	// Direct requests to the route's app's own hosts are blocked.
	if got := matchID(exports, "shop-exports.gregale.dev", "/exports/1"); got != block.ID {
		t.Fatalf("direct request = %q, want the block", got)
	}
	if got := matchID(exports, "shop-exports--8080.gregale.dev", "/admin/users"); got != selectorBlock.ID {
		t.Fatalf("selector host request = %q, want the selector block", got)
	}
}
