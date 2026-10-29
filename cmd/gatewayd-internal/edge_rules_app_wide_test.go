package main

import (
	"context"
	"testing"
	"time"

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

// adr: 1191 — an "@app" fence holds the mutated app's hosts only (ADR-091
// amendment 2026-10-27), never another app's or an unclaimed host; an
// unscoped one holds any claimed host.
func TestAppWideEdgeRuleFence(t *testing.T) {
	matcher := newGatewaydEdgeRules(state.NewMemStore(), testLogger(), nil, nil)
	matcher.BeginConvergence("acct-a", "app-shop", []string{api.EdgeRuleAppHosts}, 1)
	for _, tc := range []struct {
		host, owner, app string
		want             bool
	}{
		{"shop.example.com", "acct-a", "app-shop", true},
		{"blog.example.com", "acct-a", "app-blog", false},
		{"other.example.com", "acct-b", "app-other", false},
		{"unclaimed.example.com", "", "", false},
	} {
		if got := matcher.Converging(tc.host, tc.owner, tc.app); got != tc.want {
			t.Errorf("Converging(%s, %q, %q) = %v, want %v", tc.host, tc.owner, tc.app, got, tc.want)
		}
	}
	matcher.EndConvergence("acct-a", "app-shop", []string{api.EdgeRuleAppHosts}, 1)
	if matcher.Converging("shop.example.com", "acct-a", "app-shop") {
		t.Fatal("fence still held after EndConvergence")
	}
	// A payload without an app (an older apid) keeps the account-wide hold.
	matcher.BeginConvergence("acct-a", "", []string{api.EdgeRuleAppHosts}, 2)
	if !matcher.Converging("blog.example.com", "acct-a", "app-blog") {
		t.Fatal("app-less @app fence must hold every app of the account")
	}
	matcher.EndConvergence("acct-a", "", []string{api.EdgeRuleAppHosts}, 2)
	matcher.BeginConvergence("", "app-shop", []string{api.EdgeRuleAppHosts}, 3)
	if !matcher.Converging("shop.example.com", "acct-b", "app-b") || matcher.Converging("unclaimed.example.com", "", "") {
		t.Fatal("unscoped app-wide fence must hold claimed hosts only")
	}
}

// adr: 091 — a fence past its TTL stops holding and is dropped, and the
// request path skips the scan entirely when no fence exists.
func TestEdgeRuleFenceExpiry(t *testing.T) {
	matcher := newGatewaydEdgeRules(state.NewMemStore(), testLogger(), nil, nil)
	if matcher.Converging("shop.example.com", "acct-a", "app-shop") {
		t.Fatal("no fence must not hold")
	}
	matcher.BeginConvergence("acct-a", "", []string{"shop.example.com"}, 1)
	if !matcher.Converging("shop.example.com", "acct-a", "app-shop") || matcher.fenceCount.Load() != 1 {
		t.Fatalf("fence not held; count=%d", matcher.fenceCount.Load())
	}
	matcher.fenceMu.Lock()
	for key, fence := range matcher.fences {
		fence.expiresAt = time.Now().Add(-time.Second)
		matcher.fences[key] = fence
	}
	matcher.fenceMu.Unlock()
	if matcher.Converging("shop.example.com", "acct-a", "app-shop") {
		t.Fatal("expired fence still held")
	}
	if matcher.fenceCount.Load() != 0 {
		t.Fatalf("expired fence not dropped; count=%d", matcher.fenceCount.Load())
	}
}
