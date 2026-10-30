package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-30: a PR preview applies its production app's
// "@app" rules as well as its own, except route and async rules, so a gate
// such as kind=ip on /admin/** covers the preview's guessable hosts. Before,
// "@app" matched only the rule's own app, and previews served unprotected.
func TestPreviewAppliesProductionAppWideRules(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, _ := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	shop, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	api2, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop-api", Status: state.AppActive})
	preview, _ := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "pr-7-shop", PreviewOfSlug: "shop", Status: state.AppActive})
	create := func(appID string, kind state.EdgeRuleKind, action state.EdgeRuleAction) state.EdgeRule {
		t.Helper()
		rule, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: appID,
			MatchHost: api.EdgeRuleAppHosts, MatchPath: "/**", Priority: 10, Enabled: true, Kind: kind, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		return rule
	}
	ipRule := create(shop.ID, state.EdgeRuleKindIP, state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0"}}})
	create(shop.ID, state.EdgeRuleKindRoute, state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: api2.Slug}})
	own := create(preview.ID, state.EdgeRuleKindHeaders, appWideHeadersAction("X-Preview"))

	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	previewCtx := gateway.WithEdgeRuleScope(ctx, gateway.App{ID: preview.ID, AccountID: acct.ID, ProductionAppID: shop.ID})
	const host = "pr-7-shop.gregale.dev"

	if got := matcher.MatchIP(previewCtx, host, "/admin", "GET"); got == nil || got.ID != ipRule.ID {
		t.Fatalf("preview ip rule = %+v, want the production app's", got)
	}
	if got := matcher.MatchHeaders(previewCtx, host, "/", "GET"); got == nil || got.ID != own.ID {
		t.Fatalf("preview headers rule = %+v, want its own", got)
	}
	if got := matcher.MatchRoute(previewCtx, host, "/", "GET"); got != nil {
		t.Fatalf("preview took the production route rule %s; it must serve its own code", got.ID)
	}

	// A new production rule reaches the cached preview set through the
	// production app's "@app" invalidation.
	jwt := create(shop.ID, state.EdgeRuleKindJWT, state.EdgeRuleAction{Kind: state.EdgeRuleKindJWT, JWT: &state.EdgeRuleJWTAction{
		Issuer: "https://idp.example.com/", JWKSURL: "https://idp.example.com/jwks.json", Algorithms: []string{"RS256"}}})
	matcher.InvalidateAccountHosts(acct.ID, shop.ID, []string{api.EdgeRuleAppHosts})
	if got := matcher.MatchJWT(previewCtx, host, "/", "GET"); got == nil || got.ID != jwt.ID {
		t.Fatalf("after the production invalidation preview jwt rule = %+v, want the new one", got)
	}

	// The production app itself and other apps are unchanged.
	shopCtx := gateway.WithEdgeRuleScope(ctx, gateway.App{ID: shop.ID, AccountID: acct.ID})
	if got := matcher.MatchHeaders(shopCtx, "shop.gregale.dev", "/", "GET"); got != nil {
		t.Fatalf("production got the preview's own rule %s", got.ID)
	}
	apiCtx := gateway.WithEdgeRuleScope(ctx, gateway.App{ID: api2.ID, AccountID: acct.ID})
	if got := matcher.MatchIP(apiCtx, "shop-api.gregale.dev", "/admin", "GET"); got != nil {
		t.Fatalf("another app of the account got the production ip rule %s", got.ID)
	}
}
