package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func appWideHeadersAction(name string) state.EdgeRuleAction {
	return state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
		ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: name, Value: "1", Action: "set"}},
	}}
}

// adr: 1191 — an "@app" rule reaches every host its own app serves (custom
// domains included, in evaluation order with host rules), never another app
// of the same account, and the per-(host, app) cache keeps apps apart.
func TestAppWideEdgeRules(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	shop, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	blog, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "blog", Status: state.AppActive})
	appWide, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: shop.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 10, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-App-Wide")})
	if err != nil {
		t.Fatal(err)
	}
	hostRule, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: shop.ID,
		MatchHost: "shop.gregale.dev", MatchPath: "/**", Priority: 5, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Host")})
	if err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	serving := func(app state.App) context.Context {
		return gateway.WithEdgeRuleApp(gateway.WithEdgeRuleOwner(ctx, acct.ID), app.ID)
	}
	matchID := func(ctx context.Context, host string) string {
		if got := matcher.MatchHeaders(ctx, host, "/checkout", "GET"); got != nil {
			return got.ID
		}
		return ""
	}

	if got := matchID(serving(shop), "shop.example.com"); got != appWide.ID {
		t.Fatalf("custom domain of shop = %q, want the app-wide rule", got)
	}
	if got := matchID(serving(shop), "shop.gregale.dev"); got != hostRule.ID {
		t.Fatalf("platform host = %q, want the priority-5 host rule ahead of the app-wide one", got)
	}
	if got := matchID(serving(blog), "blog.example.com"); got != "" {
		t.Fatalf("blog (same account) got %q; an app-wide rule must not leak to another app", got)
	}
	// The same host served by another app must not reuse shop's cached set.
	if got := matchID(serving(blog), "shop.example.com"); got != "" {
		t.Fatalf("host re-served by blog got %q from shop's cache entry", got)
	}
	if got := matchID(gateway.WithEdgeRuleOwner(ctx, acct.ID), "shop.example.com"); got != "" {
		t.Fatalf("no resolved app got %q; app-wide rules need the app", got)
	}

	// A new app-wide rule appears after the "@app" invalidation.
	newer, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: blog.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 1, Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Blog")})
	if err != nil {
		t.Fatal(err)
	}
	matcher.InvalidateHosts([]string{api.EdgeRuleAppHosts})
	if got := matchID(serving(blog), "blog.example.com"); got != newer.ID {
		t.Fatalf("after invalidation blog = %q, want its new app-wide rule", got)
	}
}

// adr: 1191 — an "@app" fence holds the owning account's hosts only, and
// never an unclaimed host; an unscoped one holds any claimed host.
func TestAppWideEdgeRuleFence(t *testing.T) {
	matcher := newGatewaydEdgeRules(state.NewMemStore(), testLogger(), nil, nil)
	matcher.BeginConvergence("acct-a", []string{api.EdgeRuleAppHosts}, 1)
	for _, tc := range []struct {
		host, owner string
		want        bool
	}{
		{"shop.example.com", "acct-a", true},
		{"other.example.com", "acct-b", false},
		{"unclaimed.example.com", "", false},
	} {
		if got := matcher.Converging(tc.host, tc.owner); got != tc.want {
			t.Errorf("Converging(%s, %q) = %v, want %v", tc.host, tc.owner, got, tc.want)
		}
	}
	matcher.EndConvergence("acct-a", []string{api.EdgeRuleAppHosts}, 1)
	if matcher.Converging("shop.example.com", "acct-a") {
		t.Fatal("fence still held after EndConvergence")
	}
	matcher.BeginConvergence("", []string{api.EdgeRuleAppHosts}, 2)
	if !matcher.Converging("shop.example.com", "acct-b") || matcher.Converging("unclaimed.example.com", "") {
		t.Fatal("unscoped app-wide fence must hold claimed hosts only")
	}
}
