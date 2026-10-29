//go:build !no_pg

package migrations_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 611
func TestMigrationInvocationAttemptHistoryUpgradeAndReplay(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261004223934001)
	store := state.NewPgStore(pool)
	account, err := legacyAccount(ctx, pool, "attempt-history-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := seedHistoricalInvocationApp(t, ctx, pool, state.App{AccountID: account.ID, Slug: "attempt-history-migration", Type: state.AppTypeApp})
	// Enqueue with the staged schema's own columns: today's EnqueueInvocation
	// also reads later tables (scenario_test_action_policies, #4892).
	var inv state.Invocation
	if err := pool.QueryRow(ctx, `INSERT INTO invocations(account_id,app_id,source,due_at)
		VALUES($1,$2,$3,now()) RETURNING id::text`, account.ID, app.ID, string(state.InvocationAsyncInvoke)).Scan(&inv.ID); err != nil {
		t.Fatal(err)
	}
	// Take the legacy lease with the claim statement the pre-history release
	// ran, not today's store.ClaimInvocation: that path now also reads tables
	// from later migrations (scenario_test_action_policies, #4892) that this
	// staged schema cannot have. Seed raw, upgrade, then use the store.
	var legacyAttempts int
	if err := pool.QueryRow(ctx, `update invocations
		   set state = 'dispatching', quota_reserved = false,
		       lease_expires_at = now() + interval '60 seconds',
		       received_at = now(), attempts = attempts + 1
		 where id = $1 and state = 'pending'
		 returning attempts`, inv.ID).Scan(&legacyAttempts); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	row, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || row.State != state.InvocationDispatching || row.Attempts != legacyAttempts {
		t.Fatalf("upgrade changed lease: %+v %v", row, err)
	}
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from invocation_attempt_history").Scan(&n); err != nil || n != 0 {
		t.Fatalf("fabricated legacy history: %d %v", n, err)
	}
	if _, err := store.RequeueExpiredInvocations(ctx, time.Now().Add(2*time.Minute), 10); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimInvocation(ctx, inv.ID, "", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInvocationClaim(ctx, inv.ID, state.InvocationClaim{Attempt: claim.Attempts, ReplayGeneration: claim.ReplayGeneration}, nil); err != nil {
		t.Fatal(err)
	}
	var id int64
	var started, finished time.Time
	var outcome string
	if err := pool.QueryRow(ctx, "select id,started_at,finished_at,outcome from invocation_attempt_history where invocation_id=$1", inv.ID).Scan(&id, &started, &finished, &outcome); err != nil || outcome != "succeeded" {
		t.Fatalf("new claim: %s %v", outcome, err)
	}
	if _, err := pool.Exec(ctx, "delete from goose_db_version where version_id=20261005101735681"); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var actualID int64
	var actualStarted, actualFinished time.Time
	var actualOutcome string
	if err := pool.QueryRow(ctx, "select id,started_at,finished_at,outcome from invocation_attempt_history where invocation_id=$1", inv.ID).Scan(&actualID, &actualStarted, &actualFinished, &actualOutcome); err != nil || actualID != id || !actualStarted.Equal(started) || !actualFinished.Equal(finished) || actualOutcome != outcome {
		t.Fatalf("migration replay replaced evidence: %v", err)
	}
	row, err = store.InvocationByID(ctx, inv.ID)
	if err != nil || row.State != state.InvocationCompleted || row.Attempts != claim.Attempts {
		t.Fatalf("migration replay changed execution: %+v %v", row, err)
	}
}
