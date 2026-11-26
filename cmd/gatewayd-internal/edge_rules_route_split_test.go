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
	acct, err := store.CreateAccount(ctx, "owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	shop, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	exports, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "shop-exports", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	denyAll := state.EdgeRuleAction{Kind: state.EdgeRuleKindIP, IP: &state.EdgeRuleIPAction{Deny: []string{"0.0.0.0/0", "::/0"}}}
	rule := func(app state.App, host, path string, action state.EdgeRuleAction) state.EdgeRule {
		t.Helper()
		created, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID,
			MatchHost: host, MatchPath: path, Priority: 10, Enabled: true, Kind: action.Kind, Action: action})
		if err != nil {
			t.Fatal(err)
		}
		return created
	}
	sourceHost := rule(shop, "shop.gregale.dev", "/admin/**", denyAll)
	sourceAppWide := rule(shop, api.EdgeRuleAppHosts, "/internal/**", denyAll)
	block := rule(exports, "shop-exports.gregale.dev", "/**", denyAll)
	selectorBlock := rule(exports, "shop-exports--*.gregale.dev", "/**", denyAll)

	// The other protective kinds a source may carry on its host.
	hostRules := map[state.EdgeRuleKind]state.EdgeRule{
		state.EdgeRuleKindJWT: rule(shop, "shop.gregale.dev", "/jwt/**", state.EdgeRuleAction{Kind: state.EdgeRuleKindJWT,
			JWT: &state.EdgeRuleJWTAction{Issuer: "https://issuer.example.test", JWKSURL: "https://issuer.example.test/jwks", Algorithms: []string{"RS256"}}}),
		state.EdgeRuleKindLimit: rule(shop, "shop.gregale.dev", "/limit/**", state.EdgeRuleAction{Kind: state.EdgeRuleKindLimit,
			Limit: &state.EdgeRuleLimitAction{MaxBodyBytes: 1024}}),
		state.EdgeRuleKindThrottle: rule(shop, "shop.gregale.dev", "/throttle/**", state.EdgeRuleAction{Kind: state.EdgeRuleKindThrottle,
			Throttle: &state.EdgeRuleThrottleAction{RequestsPerSecond: 1, Burst: 1}}),
		state.EdgeRuleKindWAF: rule(shop, "shop.gregale.dev", "/waf/**", state.EdgeRuleAction{Kind: state.EdgeRuleKindWAF,
			WAF: &state.EdgeRuleWAFAction{Mode: "block"}}),
		state.EdgeRuleKindChallenge: rule(shop, "shop.gregale.dev", "/challenge/**", state.EdgeRuleAction{Kind: state.EdgeRuleKindChallenge,
			Challenge: &state.EdgeRuleChallengeAction{Difficulty: 16}}),
	}

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

	// Every protective kind on the source's host runs on a routed request.
	routed := servedAs(exports)
	const host = "shop.gregale.dev"
	matches := map[state.EdgeRuleKind]func(path string) string{
		state.EdgeRuleKindJWT: func(p string) string {
			if r := matcher.MatchJWT(routed, host, p, "GET"); r != nil {
				return r.ID
			}
			return ""
		},
		state.EdgeRuleKindLimit: func(p string) string {
			if r := matcher.MatchLimit(routed, host, p, "GET"); r != nil {
				return r.ID
			}
			return ""
		},
		state.EdgeRuleKindThrottle: func(p string) string {
			if r := matcher.MatchThrottle(routed, host, p, "GET"); r != nil {
				return r.ID
			}
			return ""
		},
		state.EdgeRuleKindWAF: func(p string) string {
			if r := matcher.MatchWAF(routed, host, p, "GET"); r != nil {
				return r.ID
			}
			return ""
		},
		state.EdgeRuleKindChallenge: func(p string) string {
			if r := matcher.MatchChallenge(routed, host, p, "GET"); r != nil {
				return r.ID
			}
			return ""
		},
	}
	for kind, match := range matches {
		if got, want := match("/"+string(kind)+"/x"), hostRules[kind].ID; got != want {
			t.Errorf("routed %s match = %q, want the source's host rule %q", kind, got, want)
		}
	}
}
