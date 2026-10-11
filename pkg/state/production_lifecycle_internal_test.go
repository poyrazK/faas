package state

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProductionLifecycleDatabaseFence(t *testing.T) {
	for _, scenario := range []string{"unfenced", "current", "configuration_changed", "capture_replaced", "capture_content_changed", "gate_changed", "scope_changed", "report_audit"} {
		t.Run(scenario, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := NewPgStore(pool)
			ctx := t.Context()
			acct, err := store.CreateAccount(ctx, "fence@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, App{AccountID: acct.ID, Slug: "fence", Type: AppTypeApp, RAMMB: 512, MaxConcurrency: 1})
			if err != nil {
				t.Fatal(err)
			}
			baseline, err := store.CreateDeployment(ctx, Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:baseline"})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDeploymentLive(ctx, baseline.ID); err != nil {
				t.Fatal(err)
			}
			candidate, err := store.CreateDeployment(ctx, Deployment{ID: uuid.NewString(), AppID: app.ID, Kind: DeploymentKindImage, ImageDigest: "sha256:candidate", TrafficPercentExplicit: true, TrafficPercent: 0})
			if err != nil {
				t.Fatal(err)
			}
			if err = store.MarkDeploymentLive(ctx, candidate.ID); err != nil {
				t.Fatal(err)
			}
			doc := []byte(`{"openapi":"3.1.0","paths":{"/health":{"get":{}}}}`)
			for _, id := range []string{baseline.ID, candidate.ID} {
				if err = store.UpsertDeploymentOpenAPIDoc(ctx, id, acct.ID, app.ID, doc, "manual_upload", false); err != nil {
					t.Fatal(err)
				}
			}
			zero := int64(0)
			if _, err = store.SaveRouteRequirements(ctx, acct.ID, app.ID, api.SaveRouteRequirementsRequest{ExpectedRevision: &zero, Requirements: api.RouteRequirementsConfig{Version: 2, Public: []api.RoutePublicException{{Method: "GET", Path: "/health", Reason: "test"}}}}); err != nil {
				t.Fatal(err)
			}
			if scenario != "report_audit" {
				if _, err = store.SetCanaryRouteGate(ctx, acct.ID, app.ID, api.SetCanaryRouteGateRequest{Mode: "enforce", ExpectedRevision: &zero}); err != nil {
					t.Fatal(err)
				}
			}
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if scenario != "unfenced" {
				if err = pgAuthorizeProductionLifecycle(ctx, tx, candidate.ID, false); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "configuration_changed":
				_, err = tx.Exec(ctx, `UPDATE apps SET consumer_auth_mode='required' WHERE id=$1`, app.ID)
			case "capture_replaced":
				_, err = tx.Exec(ctx, `UPDATE deployment_openapi_docs SET updated_at=clock_timestamp() WHERE deployment_id=$1`, candidate.ID)
			case "capture_content_changed":
				_, err = tx.Exec(ctx, `UPDATE deployment_openapi_docs SET doc='{"openapi":"3.1.0","paths":{"/health":{"get":{"x-gregale-deprecated-at":"invalid"}}}}'::jsonb WHERE deployment_id=$1`, candidate.ID)
			case "gate_changed":
				_, err = tx.Exec(ctx, `UPDATE canary_route_gates SET revision=revision+1 WHERE app_id=$1`, app.ID)
			case "scope_changed":
				_, err = tx.Exec(ctx, `UPDATE deployments SET scope='staging' WHERE id=$1`, candidate.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "scope_changed" {
				_, err = tx.Exec(ctx, `UPDATE deployments SET scope='production',traffic_percent=10 WHERE id=$1`, candidate.ID)
			} else {
				_, err = tx.Exec(ctx, `UPDATE deployments SET traffic_percent=10 WHERE id=$1`, candidate.ID)
			}
			allowed := scenario == "current" || scenario == "report_audit"
			if allowed {
				if err != nil {
					t.Fatal(err)
				}
				var found bool
				if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM production_lifecycle_reviews WHERE deployment_id=$1 AND decision->>'deployment_id'=$1::text)`, candidate.ID).Scan(&found); err != nil || !found {
					t.Fatalf("missing audit: %v %v", found, err)
				}
			} else {
				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) || pgErr.ConstraintName != "production_lifecycle_required" {
					t.Fatalf("expected transaction fence rejection: %v", err)
				}
			}
		})
	}
}

func TestProductionLifecycleRuleOrdering(t *testing.T) {
	a := RoutePolicySnapshot{Rules: []api.EdgeRuleResponse{{ID: "b", MatchPath: "/second"}, {ID: "a", MatchPath: "/first"}}}
	b := a
	b.Rules = []api.EdgeRuleResponse{a.Rules[1], a.Rules[0]}
	if lifecycleMemoryConfiguration(a) != lifecycleMemoryConfiguration(b) {
		t.Fatal("rule iteration order invalidates the receipt binding")
	}
	if a.Rules[0].ID != "b" {
		t.Fatal("configuration binding mutated the snapshot")
	}
}
