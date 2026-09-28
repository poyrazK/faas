package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// Rules load per host, and match_host is free-form: another account's rule
// on "*" at priority 0 sorted ahead of the owner's kind=jwt rule. The applier
// refuses a foreign rule, so the owner's gate never ran and the request went
// through without a token. With the request's owner recorded, the matcher
// must pick the owner's rule.
func TestForeignEdgeRuleCannotShadowOwnersGate(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	victim, _ := store.CreateAccount(ctx, "victim@example.test", api.PlanPro)
	attacker, _ := store.CreateAccount(ctx, "attacker@example.test", api.PlanPro)
	victimApp, _ := store.CreateApp(ctx, state.App{AccountID: victim.ID, Slug: "victim", Status: state.AppActive})
	attackerApp, _ := store.CreateApp(ctx, state.App{AccountID: attacker.ID, Slug: "evil", Status: state.AppActive})
	jwt := state.EdgeRuleAction{Kind: state.EdgeRuleKindJWT, JWT: &state.EdgeRuleJWTAction{
		Issuer: "https://issuer.example", JWKSURL: "https://issuer.example/jwks",
		Audience: []string{"api"}, Algorithms: []string{"RS256"},
	}}
	own, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: victim.ID, AppID: victimApp.ID,
		MatchHost: "victim.gregale.dev", MatchPath: "/admin/*", Priority: 100, Enabled: true, Kind: state.EdgeRuleKindJWT, Action: jwt})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: attacker.ID, AppID: attackerApp.ID,
		MatchHost: "*", MatchPath: "*", Priority: 0, Enabled: true, Kind: state.EdgeRuleKindJWT, Action: jwt}); err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	owned := gateway.WithEdgeRuleOwner(ctx, victim.ID)
	if got := matcher.MatchJWT(owned, "victim.gregale.dev", "/admin/users", "GET"); got == nil || got.ID != own.ID {
		t.Fatalf("MatchJWT for the victim's request = %+v, want the victim's own rule %s", got, own.ID)
	}
}
