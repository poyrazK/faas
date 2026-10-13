package main

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestSafeDeployTickCompletesNonCanaryRolloutsWithoutTokens is the #1926
// production-level check: meterd's real builder with no safe-release tokens
// selects a Postgres-backed live, non-canary deployment and completes it,
// while non-live rows are never touched (failed ones are already "aborted").
func TestSafeDeployTickCompletesNonCanaryRolloutsWithoutTokens(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "rollout-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	newDeployment := func(status string, traffic, ladder int) string {
		t.Helper()
		d, err := store.CreateDeployment(t.Context(), state.Deployment{ID: uuid.NewString(), Kind: state.DeploymentKindImage, AppID: app.ID, ImageDigest: "sha256:" + uuid.NewString()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE deployments SET status=$2, traffic_percent=$3, canary_total_steps=$4, canary_step=0,
			rollout_state='pending', rollout_started_at=NULL, rollout_completed_at=NULL WHERE id=$1`, d.ID, status, traffic, ladder); err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	live := newDeployment("live", 100, 0)
	superseded := newDeployment("superseded", 0, 0)
	failed := newDeployment("failed", 0, 0)

	deps := runDeps{getenv: func(string) string { return "" }}
	orch := buildSafeDeployOrchestrator(deps, store, wire.NewOpsMetrics("meterd_test_1926"), slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
	if orch == nil || !orch.CompleteOnly {
		t.Fatalf("without tokens meterd must still build a completion-only orchestrator, got %+v", orch)
	}
	stats, inFlight, err := orch.Once(context.Background())
	if err != nil || stats.Completed != 1 || inFlight != 1 {
		t.Fatalf("tick: stats=%+v inFlight=%d err=%v; want exactly the live row completed", stats, inFlight, err)
	}
	// A failed deployment is already fenced to "aborted" by the schema, and a
	// superseded one is not live, so the tick touches neither.
	for id, want := range map[string]string{live: "complete", superseded: "pending", failed: "aborted"} {
		var rolloutState string
		var completed bool
		if err := pool.QueryRow(t.Context(), `SELECT rollout_state, rollout_completed_at IS NOT NULL FROM deployments WHERE id=$1`, id).Scan(&rolloutState, &completed); err != nil {
			t.Fatal(err)
		}
		if rolloutState != want || completed != (want == "complete") {
			t.Fatalf("deployment %s: rollout_state=%q completed_at set=%v, want %q", id, rolloutState, completed, want)
		}
	}
	// A second tick finds nothing left to do.
	if stats, inFlight, err := orch.Once(context.Background()); err != nil || stats.Completed != 0 || inFlight != 0 {
		t.Fatalf("second tick: stats=%+v inFlight=%d err=%v", stats, inFlight, err)
	}
}
