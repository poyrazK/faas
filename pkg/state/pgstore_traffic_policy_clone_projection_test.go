//go:build !no_pg

package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficCloneProjectionRollbackAndRepair(t *testing.T) {
	for _, kind := range []string{"edge", "route", "fallback_route"} {
		t.Run(kind, func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			account, project, app := trafficProjectionOwner(t, store)
			if err := store.UpsertAppEnvInScope(ctx, account.ID, app.ID, "production", "CONFIG", "source"); err != nil {
				t.Fatal(err)
			}
			if err := store.UpsertAppSecretWithKidAndValueHashInScope(ctx, account.ID, app.ID, "production", "TOKEN", "age1", "1111111111111111", []byte("sealed")); err != nil {
				t.Fatal(err)
			}
			edge := state.ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production"}
			route := state.ProjectEnvironmentRoutePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production", OnlyAllowDeclaredRoutes: true}
			write := func(value string) error {
				if kind == "edge" {
					edge.Rules = []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
						Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
							ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Value", Value: value, Action: "set"}},
						}}}}
					_, err := store.PutProjectEnvironmentEdgePolicy(ctx, edge)
					return err
				}
				route.DeclaredRoutes = []state.DeclaredRoute{{Path: "/" + value, Methods: []string{"GET"}}}
				if kind == "fallback_route" {
					encoded, err := json.Marshal(route.DeclaredRoutes)
					if err != nil {
						return err
					}
					_, err = pool.Exec(ctx, `UPDATE apps SET declared_routes = $1, only_declared_routes = true WHERE id = $2`, encoded, app.ID)
					return err
				}
				_, err := store.PutProjectEnvironmentRoutePolicy(ctx, route)
				return err
			}
			if err := write("x"); err != nil {
				t.Fatal(err)
			}
			q := sqlc.New()
			var base []byte
			var err error
			if kind == "edge" {
				view, readErr := q.ReadPublicHostEnvironmentPolicy(ctx, pool, sqlc.ReadPublicHostEnvironmentPolicyParams{
					AccountID: pgUUID(t, account.ID), AppID: pgUUID(t, app.ID), Scope: "production", MaxBytes: api.TrafficPolicyMaxContractBytes})
				base, err = view.Data, readErr
			} else if kind == "route" {
				view, readErr := q.ReadPublicHostRoutePolicy(ctx, pool, sqlc.ReadPublicHostRoutePolicyParams{
					AccountID: pgUUID(t, account.ID), AppID: pgUUID(t, app.ID), Scope: "production", MaxBytes: api.TrafficPolicyMaxContractBytes})
				base, err = view.Data, readErr
			}
			if err != nil {
				t.Fatal(err)
			}
			valueSize := api.TrafficPolicyMaxContractBytes
			if kind != "fallback_route" {
				valueSize -= len(base) - 1
			}
			if err := write(strings.Repeat("x", valueSize)); err != nil {
				t.Fatal(err)
			}
			// The source scoped policy fits exactly. A longer target name makes
			// its complete copied projection exceed the bound.
			clone := state.ProjectEnvironmentClone{AccountID: account.ID, ProjectID: project.ID, SourceSlug: "production", TargetSlug: "longer-staging-environment"}
			_, _, err = store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan))
			scope := "environment_route_policy"
			if kind == "edge" {
				scope = "environment_edge_policy"
			}
			requireTrafficProjectionError(t, err, scope)
			if _, err := store.ProjectEnvironmentBySlug(ctx, account.ID, project.ID, clone.TargetSlug); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("refused clone committed target: %v", err)
			}
			var rows int
			if err := pool.QueryRow(ctx, `SELECT
                (SELECT count(*) FROM app_envs WHERE app_id = $1 AND scope = $2) +
                (SELECT count(*) FROM app_secrets WHERE app_id = $1 AND scope = $2) +
                (SELECT count(*) FROM project_environment_edge_policies WHERE app_id = $1 AND environment_slug = $2) +
                (SELECT count(*) FROM project_environment_route_policies WHERE app_id = $1 AND environment_slug = $2)`, app.ID, clone.TargetSlug).Scan(&rows); err != nil || rows != 0 {
				t.Fatalf("refused clone retained partial rows: count=%d err=%v", rows, err)
			}
			if err := write("repaired"); err != nil {
				t.Fatal(err)
			}
			_, result, err := store.CloneProjectEnvironment(ctx, clone, api.MustLimitsFor(account.Plan))
			if err != nil || result.VariablesCopied != 1 || result.SecretsCopied != 1 || result.RoutesCopied != 1 {
				t.Fatalf("clone after repair: result=%+v err=%v", result, err)
			}
		})
	}
}
