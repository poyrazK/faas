package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func trafficProjectionOwner(t *testing.T, store state.Store) (state.Account, state.Project, state.App) {
	t.Helper()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "traffic-projection@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "projection", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "projection-api", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	return account, project, app
}

func requireTrafficProjectionError(t *testing.T, err error, scope string) {
	t.Helper()
	var projection *state.TrafficPolicyProjectionError
	if !errors.As(err, &projection) || projection.Scope != scope ||
		projection.Limit != api.TrafficPolicyMaxContractBytes || projection.Observed <= projection.Limit {
		t.Fatalf("expected %s byte-limit error, got %v", scope, err)
	}
}

func trafficProjectionWriteRecovery(t *testing.T, store state.Store) {
	t.Helper()
	account, project, app := trafficProjectionOwner(t, store)
	ctx := context.Background()
	large := strings.Repeat("x", api.TrafficPolicyMaxContractBytes)
	limits := api.MustLimitsFor(account.Plan)

	t.Run("preset", func(t *testing.T) {
		preset := state.CorsPreset{AccountID: account.ID, AppID: app.ID, Name: "projection", AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, AllowHeaders: []string{large}}
		_, err := store.CreateCorsPresetIfUnderQuota(ctx, preset, limits)
		requireTrafficProjectionError(t, err, "cors_preset")
		rows, err := store.ListCorsPresetsForAccount(ctx, account.ID)
		if err != nil || len(rows) != 0 {
			t.Fatalf("rejected create persisted: count=%d err=%v", len(rows), err)
		}
		preset.AllowHeaders = []string{"X-Small"}
		preset, err = store.CreateCorsPresetIfUnderQuota(ctx, preset, limits)
		if err != nil {
			t.Fatal(err)
		}
		preset.AllowHeaders = []string{large}
		_, err = store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset)
		requireTrafficProjectionError(t, err, "cors_preset")
		stored, err := store.GetCorsPresetByID(ctx, account.ID, preset.ID)
		if err != nil || stored.AllowHeaders[0] != "X-Small" {
			t.Fatalf("rejected replacement changed preset: err=%v", err)
		}
		preset.AllowHeaders = nil
		if _, err := store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset); err != nil {
			t.Fatalf("smaller replacement: %v", err)
		}
		if err := store.DeleteCorsPreset(ctx, account.ID, preset.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})
	t.Run("environment_edge", func(t *testing.T) {
		policy := state.ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production"}
		if _, err := store.PutProjectEnvironmentEdgePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		policy.Rules = []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
			Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
				ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Large", Value: large, Action: "set"}},
			}}}}
		_, err := store.PutProjectEnvironmentEdgePolicy(ctx, policy)
		requireTrafficProjectionError(t, err, "environment_edge_policy")
		stored, err := store.GetProjectEnvironmentEdgePolicy(ctx, account.ID, app.ID, "production")
		if err != nil || len(stored.Rules) != 0 {
			t.Fatalf("rejected overlay changed policy: err=%v", err)
		}
		policy.Rules = nil
		if _, err := store.PutProjectEnvironmentEdgePolicy(ctx, policy); err != nil {
			t.Fatalf("empty overlay recovery: %v", err)
		}
	})
	t.Run("environment_route", func(t *testing.T) {
		policy := state.ProjectEnvironmentRoutePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID,
			EnvironmentSlug: "production", OnlyAllowDeclaredRoutes: true, DeclaredRoutes: []state.DeclaredRoute{{Path: "/health", Methods: []string{"GET"}}}}
		if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, policy); err != nil {
			t.Fatal(err)
		}
		policy.DeclaredRoutes = []state.DeclaredRoute{{Path: "/" + large, Methods: []string{"GET"}}}
		_, err := store.PutProjectEnvironmentRoutePolicy(ctx, policy)
		requireTrafficProjectionError(t, err, "environment_route_policy")
		stored, err := store.GetProjectEnvironmentRoutePolicy(ctx, account.ID, app.ID, "production")
		if err != nil || len(stored.DeclaredRoutes) != 1 || stored.DeclaredRoutes[0].Path != "/health" {
			t.Fatalf("rejected contract changed policy: err=%v", err)
		}
		policy.OnlyAllowDeclaredRoutes, policy.DeclaredRoutes = false, nil
		if _, err := store.PutProjectEnvironmentRoutePolicy(ctx, policy); err != nil {
			t.Fatalf("disabled route recovery: %v", err)
		}
	})
}

func TestMemTrafficProjectionWriteRecovery(t *testing.T) {
	trafficProjectionWriteRecovery(t, state.NewMemStore())
}
