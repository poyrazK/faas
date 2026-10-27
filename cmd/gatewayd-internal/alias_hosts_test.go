// adr: 20261026100000
// alias_hosts_test.go pins that edge rules stored under an alias apps domain
// keep applying after the apps domain moves: gatewayd-public hands
// gatewayd-internal the canonical host, so the old-host rule must match it,
// fence it and invalidate it.

package main

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

func configureSplitDomains(t *testing.T) {
	t.Helper()
	d, err := hostidentity.NewDomains("gregale.dev", "gregale.app", "gregale.app", []string{"gregale.dev"}, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hostidentity.Configure(d))
}

func TestOldHostEdgeRulesApplyToTheCanonicalHost(t *testing.T) {
	configureSplitDomains(t)
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	shop, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	oldHost, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: shop.ID,
		MatchHost: "shop.gregale.dev", MatchPath: "/**", Priority: 5, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Old")})
	if err != nil {
		t.Fatal(err)
	}
	newHost, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: shop.ID,
		MatchHost: "shop.gregale.app", MatchPath: "/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-New")})
	if err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	serving := gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, acct.ID), shop.ID)

	got := matcher.MatchHeaders(serving, "shop.gregale.app", "/", "GET")
	if got == nil || got.ID != oldHost.ID {
		t.Fatalf("canonical host = %+v, want the priority-5 rule stored under the old host", got)
	}
	// Both rules are in the compiled set, in priority order, once each.
	entry, err := matcher.loadHostUncached(serving, "shop.gregale.app")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, rule := range entry.Headers {
		ids = append(ids, rule.ID)
	}
	if !slices.Equal(ids, []string{oldHost.ID, newHost.ID}) {
		t.Fatalf("compiled header rules = %v, want old then new", ids)
	}

	// A mutation of the old-host rule drops the canonical host's entry.
	if err := store.DeleteEdgeRule(ctx, oldHost.ID); err != nil {
		t.Fatal(err)
	}
	inv := &recordingScopedInvalidator{}
	invalidateEdgeRuleScope(ctx, inv, "", shop.ID, []string{"shop.gregale.dev"})
	if !slices.Contains(inv.hosts, "shop.gregale.app") {
		t.Fatalf("invalidated hosts = %v, want the canonical host too", inv.hosts)
	}
	matcher.InvalidateHosts(inv.hosts)
	if got := matcher.MatchHeaders(serving, "shop.gregale.app", "/", "GET"); got == nil || got.ID != newHost.ID {
		t.Fatalf("after deleting the old-host rule = %+v, want the new-host rule", got)
	}
}

type recordingScopedInvalidator struct {
	edgeRuleRepairInvalidatorFake
	hosts []string
}

func (r *recordingScopedInvalidator) InvalidateEdgeRuleHosts(hosts []string) { r.hosts = hosts }
func (r *recordingScopedInvalidator) InvalidateResponseCacheByApp(string)    {}
func (r *recordingScopedInvalidator) Lookup(context.Context, string) (gateway.App, bool) {
	return gateway.App{}, false
}

func TestOldHostEdgeRuleFenceHoldsTheCanonicalHost(t *testing.T) {
	configureSplitDomains(t)
	g := newGatewaydEdgeRules(nil, nil, nil, nil)
	g.BeginConvergence("acct-a", "", []string{"shop.gregale.dev"}, 4)
	if !g.Converging("shop.gregale.app", "acct-a", "") {
		t.Fatal("an old-host rule mutation did not fence the canonical host")
	}
	g.EndConvergence("acct-a", "", []string{"shop.gregale.dev"}, 4)
	if g.Converging("shop.gregale.app", "acct-a", "") {
		t.Fatal("ending the mutation left the canonical host fenced")
	}
}

func TestCanonicalHostPatternsWithoutAliases(t *testing.T) {
	d, err := hostidentity.NewDomains("", "gregale.dev", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hostidentity.Configure(d))
	in := []string{"shop.gregale.dev", "*"}
	if got := withCanonicalHostPatterns(in); !slices.Equal(got, in) {
		t.Fatalf("withCanonicalHostPatterns = %v, want the input", got)
	}
	rules, err := withAliasHostRules(nil, "shop.gregale.dev", func(string) ([]state.EdgeRule, error) {
		t.Fatal("read an alias host with no alias configured")
		return nil, nil
	})
	if err != nil || rules != nil {
		t.Fatalf("withAliasHostRules = %v, %v", rules, err)
	}
}
