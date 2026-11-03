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

// Another tenant's rule change no longer refuses a request between its host
// pin and its (host, app) pin: the pins are checked against the invalidations
// that can reach their own sets. A change to the request's own host still
// refuses it, so its pins stay one snapshot (ADR-570).
func TestPinnedHostPolicySurvivesAnotherTenantsRuleChange(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	shop, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	g := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	const host = "shop.example.com"
	admit := func(t *testing.T) context.Context {
		t.Helper()
		hostPinned, err := g.PinHostPolicy(ctx, host)
		if err != nil {
			t.Fatal(err)
		}
		return gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(hostPinned, acct.ID), shop.ID)
	}
	admitted := admit(t)
	g.InvalidateAccountHosts("another-account", "another-app", []string{api.EdgeRuleAppHosts, "other.example.com"})
	g.InvalidateHosts([]string{"other.example.com"})
	if _, err := g.PinHostPolicy(admitted, host); err != nil {
		t.Fatalf("another tenant's rule change refused the app pin: %v", err)
	}
	admitted = admit(t)
	g.InvalidateHosts([]string{host})
	if _, err := g.PinHostPolicy(admitted, host); err == nil {
		t.Fatal("a change to the request's own host left its pins as one snapshot")
	}
}
