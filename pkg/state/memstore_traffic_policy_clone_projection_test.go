package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMemTrafficCloneProjectionPreflightAndRepair(t *testing.T) {
	for _, kind := range []string{"edge", "route", "fallback_route"} {
		t.Run(kind, func(t *testing.T) {
			store := NewMemStore()
			ctx := context.Background()
			account, err := store.CreateAccount(ctx, "clone-projection@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			project, err := store.CreateProject(ctx, Project{AccountID: account.ID, Slug: "projection", ScanSource: ProjectScanSourceCompose})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, App{AccountID: account.ID, ProjectID: project.ID, Slug: "projection-api", Status: AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "CONFIG", "source"); err != nil {
				t.Fatal(err)
			}
			seedLegacy := func(value string) {
				store.mu.Lock()
				defer store.mu.Unlock()
				key := projectEnvironmentRoutePolicyKey(app.ID, "production")
				if kind == "edge" {
					store.projectEnvironmentEdgePolicies[key] = ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID,
						EnvironmentSlug: "production", Rules: []ProjectEnvironmentEdgeRule{{Kind: EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
							Action: EdgeRuleAction{Kind: EdgeRuleKindHeaders, Headers: &EdgeRuleHeadersAction{
								ResponseHeaders: []EdgeRuleHeaderOp{{Name: "X-Value", Value: value, Action: "set"}},
							}}}}}
				} else if kind == "route" {
					store.projectEnvironmentRoutePolicies[key] = ProjectEnvironmentRoutePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID,
						EnvironmentSlug: "production", OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []DeclaredRoute{{Path: "/" + value, Methods: []string{"GET"}}}}
				} else {
					app.OnlyAllowDeclaredRoutes, app.DeclaredRoutes = true, []DeclaredRoute{{Path: "/" + value, Methods: []string{"GET"}}}
					store.apps[app.ID] = app
				}
			}
			seedLegacy(strings.Repeat("x", api.TrafficPolicyMaxContractBytes))
			clone := ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "staging"}
			_, _, err = store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan))
			var projection *TrafficPolicyProjectionError
			if !errors.As(err, &projection) || projection.Observed <= projection.Limit {
				t.Fatalf("legacy clone should refuse: %v", err)
			}
			if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, clone.TargetSlug); !errors.Is(err, ErrNotFound) {
				t.Fatalf("refused clone created target: %v", err)
			}
			if values, err := store.ListAppEnvInScope(ctx, account.ID, app.ID, clone.TargetSlug); err != nil || len(values) != 0 {
				t.Fatalf("refused clone copied variables: count=%d err=%v", len(values), err)
			}
			seedLegacy("repaired")
			_, result, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan))
			if err != nil || result.VariablesCopied != 1 || result.RoutesCopied != 1 {
				t.Fatalf("clone after repair: result=%+v err=%v", result, err)
			}
		})
	}
}
