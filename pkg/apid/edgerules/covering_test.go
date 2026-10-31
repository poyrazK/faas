package edgerules

import (
	"context"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — amendment 2026-10-31 (round 7): the gateway applies to an app's
// hosts the "*" and host-pattern rules of its account's other apps in the
// same owner scope, but the trace and the app's rule list read the app's own
// rules only. CoveringRules returns those other rules: not "@app" ones, not
// another scope's, not a deleted app's.
func TestCoveringRulesAreTheSiblingRulesTheGatewayApplies(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	acct, err := mem.CreateAccount(ctx, "covering@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := mem.CreateOrg(ctx, state.Org{Slug: "covering-acme", Name: "Acme", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	newApp := func(slug, orgID string) state.App {
		t.Helper()
		a, err := mem.CreateApp(ctx, state.App{AccountID: acct.ID, OrgID: orgID, Slug: slug, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	rule := func(app state.App, host string) state.EdgeRule {
		t.Helper()
		r, err := mem.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{AccountID: acct.ID, AppID: app.ID, MatchHost: host, MatchPath: "/**",
			Enabled: true, Kind: state.EdgeRuleKindRoute,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: app.Slug}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	app, blog, gone, company := newApp("shop", ""), newApp("blog", ""), newApp("gone", ""), newApp("acme-api", org.ID)
	rule(app, "*")
	star := rule(blog, "*")
	glob := rule(blog, "*.example.com")
	rule(blog, api.EdgeRuleAppHosts)
	rule(company, "*")
	rule(gone, "*")
	if _, err := mem.SoftDeleteAppCascade(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Host: attackModeHost{store: mem, audits: &[]string{}}}
	covering, err := svc.CoveringRules(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range covering {
		ids = append(ids, r.ID)
	}
	slices.Sort(ids)
	want := []string{star.ID, glob.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatalf("covering rules = %v, want the personal sibling's host-pattern rules %v", ids, want)
	}
}
