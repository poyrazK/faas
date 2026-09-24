// adr: 233
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentRoutingPolicyIndependentReplacement(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-routing-gateway@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "routing", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "routing-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	host := gateway.BuildEnvironmentHost(".gregale.dev", staging.ID, app.ID)
	ordinaryHost := "api.example.com"
	globalRedirect, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: app.ID, MatchHost: "*", MatchPath: "/old/*", Priority: 100,
		Enabled: true, Kind: state.EdgeRuleKindRedirect,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: 302, To: "/global/$1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	globalRewrite, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: app.ID, MatchHost: "*", MatchPath: "/rewrite/*", Priority: 100,
		Enabled: true, Kind: state.EdgeRuleKindRewrite,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRewrite, Rewrite: &state.EdgeRuleRewriteAction{From: "/rewrite/*", To: "/global/$1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	globalHeader, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: app.ID, MatchHost: "*", MatchPath: "/", Priority: 100,
		Enabled: true, Kind: state.EdgeRuleKindHeaders,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
			ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Policy", Value: "global", Action: "set"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	if got := matcher.MatchRedirect(ctx, host, "/old/item", "GET"); got == nil || got.ID != globalRedirect.ID {
		t.Fatalf("inherited redirect = %+v", got)
	}
	if got := matcher.MatchRewrite(ctx, host, "/rewrite/item", "GET"); got == nil || got.ID != globalRewrite.ID {
		t.Fatalf("inherited rewrite = %+v", got)
	}
	_, err = store.PutProjectEnvironmentRoutingPolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		Rules: []state.ProjectEnvironmentEdgeRule{
			{Kind: state.EdgeRuleKindRedirect, MatchPath: "/old/*", Priority: 10, Enabled: true,
				Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: 308, To: "/staging/$1"}}},
			{Kind: state.EdgeRuleKindRewrite, MatchPath: "/rewrite/*", Priority: 10, Enabled: true,
				Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRewrite, Rewrite: &state.EdgeRuleRewriteAction{From: "/rewrite/*", To: "/staging/$1"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset() // fleet convergence invalidates the hostname cache
	if got := matcher.MatchRedirect(ctx, host, "/old/item", "GET"); got == nil || got.ID == globalRedirect.ID || got.To != "/staging/$1" {
		t.Fatalf("scoped redirect = %+v", got)
	}
	if got := matcher.MatchRewrite(ctx, host, "/rewrite/item", "GET"); got == nil || got.ID == globalRewrite.ID || got.To != "/staging/$1" {
		t.Fatalf("scoped rewrite = %+v", got)
	}
	if got := matcher.MatchHeaders(ctx, host, "/", "GET"); got == nil || got.ID != globalHeader.ID {
		t.Fatalf("routing policy displaced headers: %+v", got)
	}
	if got := matcher.MatchRedirect(ctx, ordinaryHost, "/old/item", "GET"); got == nil || got.ID != globalRedirect.ID {
		t.Fatalf("ordinary host redirect changed: %+v", got)
	}
	_, err = store.PutProjectEnvironmentRoutingPolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging", Rules: []state.ProjectEnvironmentEdgeRule{},
	})
	if err != nil {
		t.Fatal(err)
	}
	matcher.Reset()
	if got := matcher.MatchRedirect(ctx, host, "/old/item", "GET"); got != nil {
		t.Fatalf("empty redirect override inherited global rule: %+v", got)
	}
	if got := matcher.MatchRewrite(ctx, host, "/rewrite/item", "GET"); got != nil {
		t.Fatalf("empty rewrite override inherited global rule: %+v", got)
	}
}
