//go:build !no_pg

package state_test

import (
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 233
func TestPgProjectEnvironmentRoutePolicyCloneAndDelete(t *testing.T) {
	store, ctx := pgStore(t)
	account, err := store.CreateAccount(ctx, "project-route-policy@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "route-policy", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "route-policy-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.PutProjectEnvironmentRoutePolicy(ctx, state.ProjectEnvironmentRoutePolicy{
		AccountID: account.ID, ProjectID: project.ID, AppID: app.ID,
		EnvironmentSlug: "production", OnlyAllowDeclaredRoutes: true,
		DeclaredRoutes: []state.DeclaredRoute{{Path: "/health", Methods: []string{"GET"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, result, err := store.CloneProjectEnvironment(ctx, state.ProjectEnvironmentClone{
		AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging",
	}, api.MustLimitsFor(account.Plan))
	if err != nil || result.RoutesCopied != 1 {
		t.Fatalf("clone result=%+v err=%v", result, err)
	}
	cloned, err := store.GetProjectEnvironmentRoutePolicy(ctx, account.ID, app.ID, "staging")
	if err != nil || len(cloned.DeclaredRoutes) != 1 || cloned.DeclaredRoutes[0].Path != "/health" {
		t.Fatalf("cloned route policy=%+v err=%v", cloned, err)
	}
	if _, err := store.GetProjectEnvironmentRoutePolicy(ctx, "00000000-0000-0000-0000-000000000000", app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cross-account route lookup=%v", err)
	}
	if err := store.DeleteProjectEnvironment(ctx, account.ID, project.ID, "staging"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetProjectEnvironmentRoutePolicy(ctx, account.ID, app.ID, "staging"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted route policy=%v", err)
	}
}
