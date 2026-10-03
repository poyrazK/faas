// adr: 233
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentEdgePolicyOverridesOnlyHeadersAndCORS(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-edge-gateway@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "edge", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "edge-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "edge-other", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: other.ID, MatchHost: "*", MatchPath: "/", Priority: 1,
		Enabled: true, Kind: state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Value: "other-app", Action: "set"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	host := gateway.BuildEnvironmentHost(".gregale.dev", staging.ID, app.ID)
	global, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: app.ID, MatchHost: host, MatchPath: "/", Priority: 100,
		Enabled: true, Kind: state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Value: "global", Action: "set"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	if got := matcher.MatchHeaders(ctx, host, "/", "GET"); got == nil || got.ID != global.ID {
		t.Fatalf("fallback header rule = %+v, want global", got)
	}
	_, err = store.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		Rules: []state.ProjectEnvironmentEdgeRule{
			{Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Priority: 10, Enabled: true,
				Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
					ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Environment", Value: "staging", Action: "set"}},
				}}},
			{Kind: state.EdgeRuleKindCORSA, MatchPath: "/", Priority: 20, Enabled: true,
				Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindCORSA, CORS: &state.EdgeRuleCORSAction{
					AllowOrigins: []string{"https://staging.example.com"}, AllowMethods: []string{"GET"},
				}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset() // the fleet convergence apply notification does this
	if got := matcher.MatchHeaders(ctx, host, "/", "GET"); got == nil || got.ID == global.ID || got.ResponseHeaders[0].Value != "staging" {
		t.Fatalf("scoped header rule = %+v", got)
	}
	if got := matcher.MatchCORS(ctx, host, "/", "GET"); got == nil || len(got.AllowOrigins) != 1 || got.AllowOrigins[0] != "https://staging.example.com" {
		t.Fatalf("scoped CORS rule = %+v", got)
	}
	_, err = store.PutProjectEnvironmentEdgePolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging", Rules: []state.ProjectEnvironmentEdgeRule{},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset()
	if got := matcher.MatchHeaders(ctx, host, "/", "GET"); got != nil {
		t.Fatalf("empty scoped override inherited global header: %+v", got)
	}
}
