package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 233
func TestDeclaredRoutesMatcherScopedPolicyOverlay(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "scoped-routes@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "shop", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "shop-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "staging"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, state.ProjectEnvironmentRoutePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "staging",
		OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []state.DeclaredRoute{{Path: "/staging", Methods: []string{"GET"}}},
	}); err != nil {
		t.Fatal(err)
	}
	matcher := newDeclaredRoutesMatcher(store)
	base := gateway.App{ID: app.ID, AccountID: account.ID}
	unscoped, err := matcher.ResolveScopedRoutePolicy(ctx, base)
	if err != nil || unscoped.OnlyAllowDeclaredRoutes {
		t.Fatalf("unscoped policy=%+v err=%v", unscoped, err)
	}
	staging := base
	staging.PinnedDeploymentScope = "staging"
	scoped, err := matcher.ResolveScopedRoutePolicy(ctx, staging)
	if err != nil || !scoped.OnlyAllowDeclaredRoutes || len(scoped.DeclaredRoutes) != 1 {
		t.Fatalf("scoped policy=%+v err=%v", scoped, err)
	}
	if allowed, err := matcher.MatchDeclaredRoute(ctx, scoped, "/staging", "GET"); err != nil || !allowed {
		t.Fatalf("scoped route match=%t err=%v", allowed, err)
	}
	if allowed, err := matcher.MatchDeclaredRoute(ctx, scoped, "/production", "GET"); err != nil || allowed {
		t.Fatalf("foreign route match=%t err=%v", allowed, err)
	}
}
