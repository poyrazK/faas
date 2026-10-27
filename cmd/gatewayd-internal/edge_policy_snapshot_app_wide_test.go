// adr: 570, 1191
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// On the cached path a host pinned before admission keeps its snapshot; the
// app's app-wide rules join only through the (host, app) pin, which is itself
// a snapshot that later rule changes cannot alter.
func TestPinnedHostPolicyAddsAppWideRulesOnlyThroughAppPin(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	shop, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	appWide, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: shop.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-App-Wide")})
	if err != nil {
		t.Fatal(err)
	}
	g := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	if g.RequiresOwnerPolicySnapshot() {
		t.Fatal("fixture must exercise the cached path")
	}
	const host = "shop.example.com"
	hostPinned, err := g.PinHostPolicy(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	admitted := gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(hostPinned, acct.ID), shop.ID)
	if got := g.MatchHeaders(admitted, host, "/checkout", "GET"); got != nil {
		t.Fatalf("host pin fell back to the live (host, app) cache: %+v", got)
	}
	appPinned, err := g.PinHostPolicy(admitted, host)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.MatchHeaders(appPinned, host, "/checkout", "GET"); got == nil || got.ID != appWide.ID {
		t.Fatalf("app pin = %+v, want the app-wide rule %s", got, appWide.ID)
	}
	if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: shop.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 1, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Later")}); err != nil {
		t.Fatal(err)
	}
	g.Reset()
	if got := g.MatchHeaders(appPinned, host, "/checkout", "GET"); got == nil || got.ID != appWide.ID {
		t.Fatalf("pinned app policy changed after a rule write: %+v", got)
	}
}
