//go:build !no_pg

package state

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestScenarioTestExpiryPostgres(t *testing.T) {
	// adr: 984 — persisted expiry and membership are rechecked under the registration lock.
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "scenario-expiry@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(time.Hour)
	create := func(slug, run string) App {
		t.Helper()
		app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: slug, RAMMB: 128, Status: AppActive, PreviewOfSlug: slug, PreviewPrState: PreviewPrStateOpen, PreviewExpiresAt: &future})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RegisterScenarioTestMembers(ctx, account.ID, run, []ScenarioTestMember{{Workload: slug, AppID: app.ID}}); err != nil {
			t.Fatal(err)
		}
		return app
	}
	run := "0123456789abcdef0123456789abcdef"
	first := create("expiry-api", run)
	second := create("expiry-worker", run)
	status, err := store.ScenarioTestRegistrationStatus(ctx, account.ID, run)
	if err != nil || status.ExpiryState != "pending" || status.ExpiresAt == nil || status.ExpiresAt.Sub(future).Abs() > time.Microsecond {
		t.Fatal(status, err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatal("future deadline pruned", n, err)
	}
	if _, err := pool.Exec(ctx, `update scenario_test_members set registration_expires_at=null where app_id=$1`, first.ID); err != nil {
		t.Fatal(err)
	}
	status, err = store.ScenarioTestRegistrationStatus(ctx, account.ID, run)
	if err != nil || status.ExpiryState != "unknown" || status.ExpiresAt != nil {
		t.Fatal(status, err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatal("unknown deadline pruned", n, err)
	}
	past := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if _, err := pool.Exec(ctx, `update scenario_test_members set registration_expires_at=$1 where account_id=$2 and run_id=$3`, past, account.ID, run); err != nil {
		t.Fatal(err)
	}
	candidate := scenarioTestExpiryCandidate{accountID: account.ID, runID: run, members: status.Members, expiresAt: past}
	// A new live member arriving after discovery changes both inventory and deadline.
	replacement := create("expiry-replacement", run)
	if n, err := store.pruneScenarioTestRegistration(ctx, candidate); err != nil || n != 0 {
		t.Fatal("replacement removed", n, err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatal("live member pruned", n, err)
	}
	if _, err := store.SoftDeleteAppCascade(ctx, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update scenario_test_members set registration_expires_at=$1 where account_id=$2 and run_id=$3`, past, account.ID, run); err != nil {
		t.Fatal(err)
	}
	if n, err := store.pruneScenarioTestRegistration(ctx, candidate); err != nil || n != 0 {
		t.Fatal("changed membership pruned", n, err)
	}
	// A registration transaction holding this run's lock makes the sweep skip it.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtextextended($1,0))`, account.ID+"/"+run); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatal("busy run not skipped", n, err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	busy, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Rollback(ctx)
	if _, err := busy.Exec(ctx, `select id from apps where id=$1 for update`, first.ID); err != nil {
		t.Fatal(err)
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if n, err := store.PruneScenarioTestMembers(bounded, 1); err != nil || n != 0 {
		t.Fatal("busy member not skipped", n, err)
	}
	if err := busy.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into scenario_test_chaos_matches(account_id,run_id,caller_app_id,generation,rule_id,matches)
		values($1,$2,$3,'11111111-1111-4111-8111-111111111111',$4,1)`, account.ID, run, first.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 3 {
		t.Fatal("expired deleted run", n, err)
	}
	var matches int
	if err := pool.QueryRow(ctx, `select count(*) from scenario_test_chaos_matches where account_id=$1 and run_id=$2`, account.ID, run).Scan(&matches); err != nil || matches != 0 {
		t.Fatal("expired match evidence retained", matches, err)
	}
	if n, err := store.PruneScenarioTestMembers(ctx, 1); err != nil || n != 0 {
		t.Fatal("repeat sweep", n, err)
	}
}

func TestScenarioTestExpiryMigrationBackfill(t *testing.T) {
	// adr: 984 — upgrade preserves finite leases and never invents missing deadlines.
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	store := NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "scenario-expiry-migration@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	first, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "expiry-finite", RAMMB: 128, Status: AppDeleted, PreviewExpiresAt: &future})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "expiry-unknown", RAMMB: 128, Status: AppDeleted})
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("../../migrations/20261016123516001_scenario_test_registration_expiry.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("missing down migration")
	}
	if _, err := pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	run := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, member := range []ScenarioTestMember{{AppID: first.ID, Workload: "finite"}, {AppID: second.ID, Workload: "unknown"}} {
		if _, err := pool.Exec(ctx, `insert into scenario_test_members(account_id,run_id,workload_name,app_id) values($1,$2,$3,$4)`, account.ID, run, member.Workload, member.AppID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	status, err := store.ScenarioTestRegistrationStatus(ctx, account.ID, run)
	if err != nil || status.ExpiryState != "unknown" || status.ExpiresAt != nil || len(status.Members) != 2 {
		t.Fatal(status, err)
	}
	if _, err := pool.Exec(ctx, `delete from scenario_test_members where app_id=$1`, second.ID); err != nil {
		t.Fatal(err)
	}
	status, err = store.ScenarioTestRegistrationStatus(ctx, account.ID, run)
	if err != nil || status.ExpiresAt == nil || !status.ExpiresAt.Equal(future) {
		t.Fatal(status, err)
	}
	if _, err := pool.Exec(ctx, `update scenario_test_members set registration_expires_at='infinity' where app_id=$1`, first.ID); err == nil {
		t.Fatal("infinite registration deadline accepted")
	}
}
