package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (round 6): a company app keeps its
// creator's account, and rules were kept to the host owner's account only, so
// a "*" redirect on the creator's personal app took over the company app's
// hosts, which an offboarded creator kept doing. A host's rules now come only
// from apps in its app's edge-rule scope.
func TestEdgeRulesStayInTheirAppsScope(t *testing.T) {
	mem := state.NewMemStore()
	ctx := t.Context()
	creator, err := mem.CreateAccount(ctx, "creator@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := mem.CreateOrg(ctx, state.Org{Slug: "acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, orgID string) state.App {
		t.Helper()
		a, err := mem.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	personal := newApp("alice-own", "")
	personal2 := newApp("alice-blog", "")
	company := newApp("acme-api", org.ID)
	if _, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: creator.ID, AppID: personal.ID,
		MatchHost: "*", MatchPath: "/**", Priority: 1, Enabled: true, Kind: state.EdgeRuleKindRedirect,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: 302, To: "https://evil.example/"}}}); err != nil {
		t.Fatal(err)
	}
	g := newGatewaydEdgeRules(mem, newQuietLogger(), nil, nil)
	for _, tc := range []struct {
		name string
		app  state.App
		host string
		want bool
	}{
		{"own app", personal, "alice-own.gregale.app", true},
		{"another personal app", personal2, "alice-blog.gregale.app", true},
		{"company app", company, "acme-api.gregale.app", false},
		{"company custom domain", company, "api.acme.com", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scoped := gateway.WithEdgeRuleScope(ctx, gateway.App{ID: tc.app.ID, AccountID: creator.ID})
			if got := g.MatchRedirect(scoped, tc.host, "/login", "GET") != nil; got != tc.want {
				t.Fatalf("personal app's \"*\" redirect applies on %s = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}
