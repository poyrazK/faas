//go:build !no_pg

// adr: 570
package migrations_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationInvocationQueueBindingIdentity(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261001080000003)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "invocation-binding-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := seedHistoricalInvocationApp(t, ctx, pool, state.App{AccountID: account.ID, Slug: "invocation-binding-migration", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	binding := func(name string) state.QueueBindingConsumerResult {
		t.Helper()
		b := seedLegacyQueueBindingConsumer(t, pool, account.ID, app.ID, name, false)
		return b
	}
	stable, renamed, conflict := binding("stable"), binding("before"), binding("conflict")
	seed := func(name string, created time.Time) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `insert into invocations(app_id,account_id,source,queue_name,payload,headers,due_at,created_at)
   values($1,$2,'queue',$3,'{}','{}',now(),$4) returning id`, app.ID, account.ID, name, created).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	stableID := seed("stable", time.Now())
	oldNameID := seed("before", time.Now())
	ambiguousTimeID := seed("conflict", time.Now().Add(-time.Hour))
	receiptID := seed("", time.Now())
	conflictingID := seed("conflict", time.Now())
	lateLegacyID := seed("future", time.Now().Add(time.Hour))
	if _, err := store.InsertTriggerRecord(ctx, renamed.Changes[0].TriggerID, receiptID, []byte(`{}`), []byte(`{}`), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	// A receipt and the current label disagree: this row must remain unassigned.
	if _, err := store.InsertTriggerRecord(ctx, stable.Changes[0].TriggerID, conflictingID, []byte(`{}`), []byte(`{}`), []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	newName := "after"
	if _, err := pool.Exec(ctx, `update queue_bindings set queue_name=$2,updated_at=now() where id=$1;`, renamed.Binding.ID, newName); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update triggers set slug=$2 where id=$1`, renamed.Changes[0].TriggerID, newName); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, want string }{
		{stableID, stable.Binding.ID}, {oldNameID, ""}, {ambiguousTimeID, ""}, {receiptID, renamed.Binding.ID}, {conflictingID, ""}, {lateLegacyID, ""},
	} {
		row, err := store.InvocationByID(ctx, tc.id)
		if err != nil || row.QueueBindingID != tc.want {
			t.Fatalf("historical capture id=%s owner=%q want=%q err=%v", tc.id, row.QueueBindingID, tc.want, err)
		}
	}
	for _, tc := range []struct {
		name, query, constraint string
		args                    []any
	}{
		{"clear owner", `update invocations set queue_binding_id=null where id=$1`, "invocation_queue_binding_identity", []any{stableID}},
		{"move owner", `update invocations set queue_binding_id=$2 where id=$1`, "invocation_queue_binding_identity", []any{stableID, conflict.Binding.ID}},
		{"rewrite label", `update invocations set queue_name='other' where id=$1`, "invocation_queue_binding_identity", []any{stableID}},
		{"adopt ambiguous", `update invocations set queue_binding_id=$2 where id=$1`, "invocation_queue_binding_identity", []any{oldNameID, renamed.Binding.ID}},
		{"change source", `update invocations set source='async_invoke' where id=$1`, "invocation_queue_binding_identity", []any{stableID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.query, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != tc.constraint {
				t.Fatalf("identity guard=%v", err)
			}
		})
	}
	future := binding("future")
	// Reapplying the migration cannot adopt legacy rows accepted before a binding
	// existed, even when their timestamp/name would now match its capture query.
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20261001081007501`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if row, err := store.InvocationByID(ctx, lateLegacyID); err != nil || row.QueueBindingID != "" {
		t.Fatalf("replay silently adopted legacy work=%+v %v", row, err)
	}
	fresh, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: "future", DueAt: time.Now()})
	if err != nil || fresh.QueueBindingID != future.Binding.ID {
		t.Fatalf("new admission capture=%+v %v", fresh, err)
	}
}
