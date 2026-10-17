//go:build !no_pg

// adr: 570
package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationQueueConsumerBindingIdentityAdoptionAndGuards(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261001070000001)
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-identity-migration@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app := seedHistoricalInvocationApp(t, ctx, pool, state.App{AccountID: account.ID, Slug: "queue-identity-migration", WorkloadClass: state.WorkloadClassWorker})
	binding := func(name string) state.QueueBinding {
		t.Helper()
		// Seed the historical schema before retirement exists.
		row := state.QueueBinding{}
		err := pool.QueryRow(ctx, `insert into queue_bindings(account_id,app_id,name,queue_name,mode,workload_class,enabled,max_concurrency)
        values($1,$2,$3,$3,'push','worker',true,1) returning id`, account.ID, app.ID, name).Scan(&row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	owned, ambiguous, unprojected := binding("owned"), binding("ambiguous"), binding("unprojected")
	legacyTrigger := func(slug, marker string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `insert into triggers(account_id,app_id,kind,source,slug,enabled,config)
		values($1,$2,'queue','queue',$3,false,jsonb_build_object('mode','queue','queue_binding_id',$4::text)) returning id`,
			account.ID, app.ID, slug, marker).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	ownedID := legacyTrigger("owned", owned.ID)
	firstAmbiguous := legacyTrigger("ambiguous-first", ambiguous.ID)
	secondAmbiguous := legacyTrigger("ambiguous-second", ambiguous.ID)
	malformed := legacyTrigger("malformed", "not-a-uuid")
	receipt, err := store.InsertTriggerRecord(ctx, ownedID, "receipt", []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, want string }{
		{ownedID, owned.ID}, {firstAmbiguous, ""}, {secondAmbiguous, ""}, {malformed, ""},
	} {
		var actual string
		if err := pool.QueryRow(ctx, `select coalesce(queue_binding_id::text,'') from triggers where id=$1`, tc.id).Scan(&actual); err != nil || actual != tc.want {
			t.Fatalf("adoption id=%s binding=%q want=%q err=%v", tc.id, actual, tc.want, err)
		}
	}
	if got, err := store.TriggerRecordIDByItemIdentifier(ctx, ownedID, "receipt"); err != nil || got != receipt {
		t.Fatalf("adoption lost receipt: id=%q err=%v", got, err)
	}
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, ambiguous.ID, state.UpdateQueueBindingParams{}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("ambiguous projection silently adopted: %v", err)
	}
	other, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "other-queue-app", WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(ctx, "foreign-queue-identity@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query, code, constraint string
		args                          []any
	}{
		{"clear identity", `update triggers set queue_binding_id=null where id=$1`, "23514", "queue_consumer_binding_identity", []any{ownedID}},
		{"move consumer", `update triggers set app_id=$2 where id=$1`, "23514", "queue_consumer_binding_identity", []any{ownedID, other.ID}},
		{"move tenant", `update triggers set account_id=$2 where id=$1`, "23514", "queue_consumer_binding_identity", []any{ownedID, foreign.ID}},
		{"change marker", `update triggers set config='{"mode":"queue"}' where id=$1`, "23514", "triggers_queue_binding_projection_check", []any{ownedID}},
		{"change mode", `update triggers set config=jsonb_set(config,'{mode}','"delayed_task"') where id=$1`, "23514", "triggers_queue_binding_projection_check", []any{ownedID}},
		{"delete owned binding", `delete from queue_bindings where id=$1`, "23503", "triggers_queue_binding_identity_fk", []any{owned.ID}},
		{"cross app owner", `insert into triggers(account_id,app_id,kind,source,slug,queue_binding_id,config)
		values($1,$2,'queue','queue','cross-app',$3::uuid,jsonb_build_object('mode','queue','queue_binding_id',$3::uuid::text))`,
			"23503", "triggers_queue_binding_identity_fk", []any{account.ID, other.ID, unprojected.ID}},
		{"cross tenant owner", `insert into triggers(account_id,app_id,kind,source,slug,queue_binding_id,config)
		values($1,$2,'queue','queue','cross-tenant',$3::uuid,jsonb_build_object('mode','queue','queue_binding_id',$3::uuid::text))`,
			"23503", "triggers_queue_binding_identity_fk", []any{foreign.ID, app.ID, unprojected.ID}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.query, tc.args...)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.code || pgErr.ConstraintName != tc.constraint {
				t.Fatalf("guard failure = %v, want %s/%s", err, tc.code, tc.constraint)
			}
		})
	}
	// The deferred ownership FK does not obstruct the established parent-app
	// cascade: both the binding and its consumer disappear in that transaction.
	if _, err := pool.Exec(ctx, `delete from apps where id=$1`, app.ID); err != nil {
		t.Fatalf("parent cascade: %v", err)
	}
}
