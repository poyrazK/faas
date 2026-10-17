// adr: 570, 1191
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// The authoritative owner policy is what production gateways enforce, so an
// app's "@app" rules must be compiled into it, and another app's must not.
func TestPublicEdgePolicyPostgresIncludesOwnAppWideRules(t *testing.T) {
	f := newPublicRoutingPGFixture(t)
	f.deployment(t, "production", "sha256:app-wide")
	host := f.app.Slug + ".apps.gregale.dev"
	own := f.edgeRule(t, api.EdgeRuleAppHosts, appWideHeadersAction("X-App-Wide"))
	project, err := f.store.CreateProject(t.Context(), state.Project{AccountID: f.app.AccountID, Slug: "public-routing-other"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.store.CreateApp(t.Context(), state.App{AccountID: f.app.AccountID, ProjectID: project.ID, Slug: "public-routing-other",
		Type: state.AppTypeApp, RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.store.CreateEdgeRule(t.Context(), state.CreateEdgeRuleParams{AccountID: f.app.AccountID, AppID: other.ID,
		MatchHost: api.EdgeRuleAppHosts, MatchPath: "/*", Enabled: true, Kind: state.EdgeRuleKindHeaders, Action: appWideHeadersAction("X-Other-App")})
	if err != nil {
		t.Fatal(err)
	}
	g := newGatewaydEdgeRules(f.store, nil, nil, nil)
	if !g.RequiresOwnerPolicySnapshot() {
		t.Fatal("fixture must exercise the authoritative owner policy")
	}
	ctx, app := f.compiledApp(t, g, host)
	entry := app.PublicCompiledPolicies[host]
	if entry == nil || len(entry.Headers) != 1 || entry.Headers[0].ID != own.ID {
		t.Fatalf("owner policy headers = %+v, want only %s (not %s)", entry, own.ID, foreign.ID)
	}
	if got := g.MatchHeaders(gateway.WithEdgeRuleApp(ctx, app.ID), host, "/checkout", "GET"); got == nil || got.ID != own.ID {
		t.Fatalf("request matcher = %+v, want the app-wide rule %s", got, own.ID)
	}
}
