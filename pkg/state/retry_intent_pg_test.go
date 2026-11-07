//go:build !no_pg

// adr: 117 — PostgreSQL preserves retry intent across old stage writers.
package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresRetryIntentSurvivesLegacyStageWrites(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "retry-stage-upgrade@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "retry-stage-upgrade", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:reviewed", TrafficPercent: 10, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	// Old binaries serialize their smaller StageState and omit new members.
	for _, stage := range []string{`{"current":"image_build","history":[]}`, `{"current":"image_build","history":[],"retry_intent":{"version":"v1","traffic_percent":100}}`} {
		if _, err := pool.Exec(ctx, `UPDATE deployments SET stage_state=$2::jsonb WHERE id=$1`, dep.ID, stage); err != nil {
			t.Fatal(err)
		}
		got, err := store.DeploymentByID(ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		var stages state.StageState
		if err := json.Unmarshal(got.StageState, &stages); err != nil || stages.RetryIntent == nil || stages.RetryIntent.TrafficPercent != 10 {
			t.Fatalf("stage writer changed intent: %s %v", got.StageState, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET status='failed',traffic_percent=0,rollout_state='aborted',stage_state='{"current":"","history":[]}'::jsonb WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild)
	if err != nil || retry.TrafficPercent != 10 {
		t.Fatalf("legacy failure lost intent: %+v %v", retry, err)
	}
	// Simulate a row already terminal before this migration. Its lost explicit
	// percentage cannot be recovered by looking at the failed serving weight.
	if _, err := pool.Exec(ctx, `ALTER TABLE deployments DISABLE TRIGGER deployments_retry_intent`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET stage_state=stage_state-'retry_intent' WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE deployments ENABLE TRIGGER deployments_retry_intent`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild); !errors.Is(err, state.ErrRetryIntentUnavailable) {
		t.Fatalf("guessed lost legacy traffic: %v", err)
	}
}

func TestPostgresRetryIntentCapturesLegacyFailureInput(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "retry-before-upgrade@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "retry-before-upgrade", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:reviewed", TrafficPercent: 10, TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE deployments DISABLE TRIGGER deployments_retry_intent`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE deployments SET stage_state=stage_state-'retry_intent' WHERE id=$1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE deployments ENABLE TRIGGER deployments_retry_intent`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetDeploymentFailed(ctx, dep.ID, api.CodeDeployFailed, "old deployment failed after upgrade"); err != nil {
		t.Fatal(err)
	}
	retry, err := store.RetryDeploymentFromStage(ctx, dep.ID, state.StageImageBuild)
	if err != nil || retry.TrafficPercent != 10 {
		t.Fatalf("failure captured cleared weight instead of prior intent: %+v %v", retry, err)
	}
}
