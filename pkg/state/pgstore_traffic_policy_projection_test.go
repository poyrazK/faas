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

func TestPgTrafficProjectionWriteRecovery(t *testing.T) {
	store, _ := pgStore(t)
	trafficProjectionWriteRecovery(t, store)
}

func TestPgTrafficProjectionEnvironmentCanonicalBoundariesAndLegacyRepair(t *testing.T) {
	for _, kind := range []string{"edge", "route"} {
		t.Run(kind, func(t *testing.T) {
			store, pool, ctx := pgStoreWithPool(t)
			account, project, app := trafficProjectionOwner(t, store)
			q := sqlc.New()
			edge := state.ProjectEnvironmentEdgePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production"}
			route := state.ProjectEnvironmentRoutePolicy{AccountID: account.ID, ProjectID: project.ID, AppID: app.ID, EnvironmentSlug: "production", OnlyAllowDeclaredRoutes: true}
			payload := func(value string) any {
				if kind == "edge" {
					return []state.ProjectEnvironmentEdgeRule{{Kind: state.EdgeRuleKindHeaders, MatchPath: "/", Enabled: true,
						Action: state.EdgeRuleAction{Kind: state.EdgeRuleKindHeaders, Headers: &state.EdgeRuleHeadersAction{
							ResponseHeaders: []state.EdgeRuleHeaderOp{{Name: "X-Value", Value: value, Action: "set"}},
						}}}}
				}
				return []state.DeclaredRoute{{Path: "/" + value, Methods: []string{"GET"}}}
			}
			write := func(value string) error {
				if kind == "edge" {
					edge.Rules = payload(value).([]state.ProjectEnvironmentEdgeRule)
					_, err := store.PutProjectEnvironmentEdgePolicy(ctx, edge)
					return err
				}
				route.DeclaredRoutes = payload(value).([]state.DeclaredRoute)
				_, err := store.PutProjectEnvironmentRoutePolicy(ctx, route)
				return err
			}
			read := func() ([]byte, bool, error) {
				if kind == "edge" {
					view, err := q.ReadPublicHostEnvironmentPolicy(ctx, pool, sqlc.ReadPublicHostEnvironmentPolicyParams{
						AccountID: pgUUID(t, account.ID), AppID: pgUUID(t, app.ID), Scope: "production", MaxBytes: api.TrafficPolicyMaxContractBytes})
					return view.Data, view.Oversized, err
				}
				view, err := q.ReadPublicHostRoutePolicy(ctx, pool, sqlc.ReadPublicHostRoutePolicyParams{
					AccountID: pgUUID(t, account.ID), AppID: pgUUID(t, app.ID), Scope: "production", MaxBytes: api.TrafficPolicyMaxContractBytes})
				return view.Data, view.Oversized, err
			}
			// A nonempty value keeps the header's omitempty field present
			// while calibrating the canonical projection's fixed overhead.
			if err := write("x"); err != nil {
				t.Fatal(err)
			}
			data, oversized, err := read()
			if err != nil || oversized {
				t.Fatalf("initial projection: oversized=%v err=%v", oversized, err)
			}
			value := strings.Repeat("x", api.TrafficPolicyMaxContractBytes-len(data)+1)
			if err := write(value); err != nil {
				t.Fatalf("canonical payload at bound rejected: %v", err)
			}
			data, oversized, err = read()
			if err != nil || oversized || len(data) != api.TrafficPolicyMaxContractBytes {
				t.Fatalf("boundary differs from runtime: bytes=%d oversized=%v err=%v", len(data), oversized, err)
			}
			var projection *state.TrafficPolicyProjectionError
			if err := write(value + "x"); !errors.As(err, &projection) || projection.Observed != api.TrafficPolicyMaxContractBytes+1 {
				t.Fatalf("one byte over canonical bound: %v", err)
			}
			encoded, err := json.Marshal(payload(value + "x"))
			if err != nil {
				t.Fatal(err)
			}
			if kind == "edge" {
				_, err = pool.Exec(ctx, `UPDATE project_environment_edge_policies SET rules = $1 WHERE app_id = $2`, encoded, app.ID)
			} else {
				_, err = pool.Exec(ctx, `UPDATE project_environment_route_policies SET declared_routes = $1 WHERE app_id = $2`, encoded, app.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			data, oversized, err = read()
			if err != nil || !oversized || data != nil {
				t.Fatalf("legacy row did not refuse runtime: oversized=%v err=%v", oversized, err)
			}
			if kind == "edge" {
				edge.Rules = nil
				_, err = store.PutProjectEnvironmentEdgePolicy(ctx, edge)
			} else {
				route.OnlyAllowDeclaredRoutes, route.DeclaredRoutes = false, nil
				_, err = store.PutProjectEnvironmentRoutePolicy(ctx, route)
			}
			if err != nil {
				t.Fatalf("empty legacy repair: %v", err)
			}
			data, oversized, err = read()
			if err != nil || oversized || data == nil {
				t.Fatalf("fresh runtime did not recover: oversized=%v err=%v", oversized, err)
			}
		})
	}
}

func TestPgTrafficProjectionCanonicalBoundaryAndLegacyRepair(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, _, app := trafficProjectionOwner(t, store)
	preset, err := store.CreateCorsPresetIfUnderQuota(ctx, state.CorsPreset{AccountID: account.ID, AppID: app.ID,
		Name: "boundary", AllowOrigins: []string{"*"}, AllowMethods: []string{"GET"}, AllowHeaders: []string{""}}, api.MustLimitsFor(account.Plan))
	if err != nil {
		t.Fatal(err)
	}
	q := sqlc.New()
	params := sqlc.ReadPublicHostCorsPresetParams{AccountID: pgUUID(t, account.ID), PresetID: pgUUID(t, preset.ID), MaxBytes: api.TrafficPolicyMaxContractBytes}
	view, err := q.ReadPublicHostCorsPreset(ctx, pool, params)
	if err != nil || view.Oversized {
		t.Fatalf("initial runtime projection: %v oversized=%v", err, view.Oversized)
	}
	preset.AllowHeaders = []string{strings.Repeat("x", api.TrafficPolicyMaxContractBytes-len(view.Data))}
	if _, err := store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset); err != nil {
		t.Fatalf("canonical payload at bound rejected: %v", err)
	}
	view, err = q.ReadPublicHostCorsPreset(ctx, pool, params)
	if err != nil || view.Oversized || len(view.Data) != api.TrafficPolicyMaxContractBytes {
		t.Fatalf("boundary differs from runtime: bytes=%d oversized=%v err=%v", len(view.Data), view.Oversized, err)
	}
	preset.AllowHeaders = []string{preset.AllowHeaders[0] + "x"}
	_, err = store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset)
	var projection *state.TrafficPolicyProjectionError
	if !errors.As(err, &projection) || projection.Observed != api.TrafficPolicyMaxContractBytes+1 {
		t.Fatalf("one byte over canonical bound: %v", err)
	}
	// Bypass the guarded customer mutation to model an oversized pre-upgrade row.
	if _, err := pool.Exec(ctx, `UPDATE cors_presets SET allow_headers = $1 WHERE id = $2`, preset.AllowHeaders, preset.ID); err != nil {
		t.Fatal(err)
	}
	view, err = q.ReadPublicHostCorsPreset(ctx, pool, params)
	if err != nil || !view.Oversized || view.Data != nil {
		t.Fatalf("legacy row did not refuse runtime: oversized=%v err=%v", view.Oversized, err)
	}
	preset.AllowHeaders = []string{"X-Repaired"}
	if _, err := store.UpdateCorsPreset(ctx, account.ID, preset.ID, preset); err != nil {
		t.Fatalf("repair oversized legacy row: %v", err)
	}
	view, err = q.ReadPublicHostCorsPreset(ctx, pool, params)
	if err != nil || view.Oversized || !strings.Contains(string(view.Data), "X-Repaired") {
		t.Fatalf("fresh runtime did not recover: oversized=%v err=%v", view.Oversized, err)
	}
}
