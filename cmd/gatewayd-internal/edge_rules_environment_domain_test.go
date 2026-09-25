package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentBoundCustomDomainUsesScopedEdgeRules(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "environment-domain-edge@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "domain-edge"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "domain-edge-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := store.CreateCustomDomainIfUnderQuota(ctx, "stage-edge.example.test", app.ID, "token", 10, 10, environment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDomainVerified(ctx, domain.Domain); err != nil {
		t.Fatal(err)
	}
	global, err := store.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID: account.ID, AppID: app.ID, MatchHost: "*", MatchPath: "/old/*", Priority: 100,
		Enabled: true, Kind: state.EdgeRuleKindRedirect,
		Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: 302, To: "/global/$1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentRoutingPolicy(ctx, state.ProjectEnvironmentEdgePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		Rules: []state.ProjectEnvironmentEdgeRule{{
			Kind: state.EdgeRuleKindRedirect, MatchPath: "/old/*", Priority: 10, Enabled: true,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindRedirect, Redirect: &state.EdgeRuleRedirectAction{StatusCode: 308, To: "/stage/$1"}},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	matcher := newGatewaydEdgeRules(store, testLogger(), nil, nil)
	if got := matcher.MatchRedirect(ctx, domain.Domain, "/old/item", "GET"); got == nil || got.ID == global.ID || got.To != "/stage/$1" {
		t.Fatalf("bound domain redirect = %+v", got)
	}
	if got := matcher.MatchRedirect(ctx, "ordinary.example.test", "/old/item", "GET"); got == nil || got.ID != global.ID {
		t.Fatalf("ordinary hostname redirect = %+v", got)
	}
}
