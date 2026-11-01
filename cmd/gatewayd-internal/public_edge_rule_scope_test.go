// adr: 570, 091
package main

import (
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (round 7): the authoritative host policy
// read a host's rules without the cached path's later filters, so turning on
// FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY brought back three fixed bugs. Its
// compiled policy now leaves out another owner scope's "*" rules and a deleted
// app's, and keeps a personal sibling's.
func TestPublicEdgePolicyPostgresKeepsTheCachedPathsRuleFilters(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:scope")
	host := f.app.Slug + ".apps.gregale.dev"
	ctx := t.Context()
	org, err := f.store.CreateOrg(ctx, state.Org{Slug: "public-routing-acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	sibling := func(slug, orgID string) state.App {
		t.Helper()
		app, err := f.store.CreateApp(ctx, state.App{AccountID: f.app.AccountID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 128})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	star := func(app state.App, header string) state.EdgeRule {
		t.Helper()
		rule, err := f.store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: f.app.AccountID, AppID: app.ID,
			MatchHost: "*", MatchPath: "/*", Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction(header)})
		if err != nil {
			t.Fatal(err)
		}
		return rule
	}
	personal := star(sibling("public-routing-blog", ""), "X-Personal")
	company := star(sibling("public-routing-company", org.ID), "X-Company")
	gone := sibling("public-routing-gone", "")
	deleted := star(gone, "X-Deleted")
	if _, err := f.store.SoftDeleteAppCascade(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	if !g.RequiresOwnerPolicySnapshot() {
		t.Fatal("fixture must exercise the authoritative owner policy")
	}
	_, app := f.compiledApp(t, g, host)
	entry := app.PublicCompiledPolicies[host]
	if entry == nil {
		t.Fatal("no compiled owner policy for the host")
	}
	var ids []string
	for _, rule := range entry.Headers {
		ids = append(ids, rule.ID)
	}
	if !slices.Contains(ids, personal.ID) || slices.Contains(ids, company.ID) || slices.Contains(ids, deleted.ID) {
		t.Fatalf("owner policy headers = %v; want %s (personal sibling), not %s (company) or %s (deleted app)", ids, personal.ID, company.ID, deleted.ID)
	}
}

// A preview's authoritative policy carries its production app's app-wide
// rules that previews apply, as the cached path does (amendment 2026-10-30).
func TestPublicPreviewInheritedRulesPostgres(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	ctx := t.Context()
	gate := f.edgeRule(t, api.EdgeRuleAppHosts, appWideHeadersAction("X-Production"))
	if _, err := f.store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: f.app.AccountID, AppID: f.app.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/*", Enabled: true, Kind: state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: f.app.Slug}}}); err != nil {
		t.Fatal(err)
	}
	var inherited, none []state.EdgeRule
	if err := f.store.WithPublicHostPolicySnapshot(ctx, func(reader state.PublicHostPolicyReader) error {
		var readErr error
		// The resolved preview carries its production app's ID.
		if inherited, readErr = publicPreviewInheritedRules(ctx, reader, "public-routing-pr-7.apps.gregale.dev", f.app.AccountID, f.app.ID); readErr != nil {
			return readErr
		}
		none, readErr = publicPreviewInheritedRules(ctx, reader, f.app.Slug+".apps.gregale.dev", f.app.AccountID, "")
		return readErr
	}); err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("an app that is no preview inherited %+v", none)
	}
	if len(inherited) != 1 || inherited[0].ID != gate.ID {
		t.Fatalf("inherited = %+v, want only the production app-wide headers rule %s (not its route)", inherited, gate.ID)
	}
}
