//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-20261024100845: the rehearsal guards on invocations run only on a
// claim, the managed-work capture reads the policy once per update, and the
// migration rolls back to the unguarded triggers.
func TestRehearsalInvocationTriggerGuardsMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	acct, err := store.CreateAccount(ctx, "trigger-guards@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "trigger-guards", Type: state.AppTypeApp, RAMMB: 512, IdleTimeoutS: 60, MaxConcurrency: 5})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261024100845870_rehearsal_invocation_trigger_guards.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	triggers := func() map[string]string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT tgname, pg_get_triggerdef(oid) FROM pg_trigger
			WHERE tgrelid='invocations'::regclass AND tgname LIKE 'scenario_%'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]string{}
		for rows.Next() {
			var name, def string
			if err := rows.Scan(&name, &def); err != nil {
				t.Fatal(err)
			}
			out[name] = def
		}
		return out
	}
	singleLookup := func() bool {
		t.Helper()
		var guarded bool
		if err := pool.QueryRow(ctx, `SELECT prosrc LIKE '%NEW.app_id IS DISTINCT FROM OLD.app_id%' FROM pg_proc WHERE proname='scenario_managed_work_capture'`).Scan(&guarded); err != nil {
			t.Fatal(err)
		}
		return guarded
	}
	// An ordinary app's invocation still moves through its lifecycle.
	lifecycle := func() {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO invocations(app_id, account_id, source, state, method, path, payload, headers, due_at)
			VALUES ($1, $2, 'async_invoke', 'pending', 'POST', '/job', '{}', '{}', now()) RETURNING id::text`, app.ID, acct.ID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		for _, update := range []string{
			`UPDATE invocations SET state='dispatching', attempts=attempts+1, instance_id=gen_random_uuid()::text WHERE id=$1`,
			`UPDATE invocations SET state='completed', completed_at=now() WHERE id=$1`,
		} {
			if _, err := pool.Exec(ctx, update, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	guarded := func() {
		t.Helper()
		got := triggers()
		const claim = "WHEN (((new.state = 'dispatching'::text) AND"
		if !strings.Contains(got["scenario_suspend_dispatch_guard"], claim) || !strings.Contains(got["scenario_postgres_fault_scope_dispatch"], claim) ||
			!strings.Contains(got["scenario_postgres_fault_scope"], "BEFORE INSERT ON") || !singleLookup() {
			t.Fatalf("guards missing: %v", got)
		}
		lifecycle()
	}
	guarded()
	if _, err := pool.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	got := triggers()
	if _, ok := got["scenario_postgres_fault_scope_dispatch"]; ok || strings.Contains(got["scenario_suspend_dispatch_guard"], "WHEN") ||
		!strings.Contains(got["scenario_postgres_fault_scope"], "BEFORE INSERT OR UPDATE OF state, attempts, instance_id") || singleLookup() {
		t.Fatalf("rollback left guards: %v", got)
	}
	lifecycle()
	if _, err := pool.Exec(ctx, up); err != nil {
		t.Fatal(err)
	}
	guarded()
}
