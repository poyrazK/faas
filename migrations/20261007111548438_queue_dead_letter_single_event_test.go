//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// production-us hunt #5 (H5-28): one exhausted queue message appeared twice in
// the dead-letter ledger, once as its invocation and once as the private
// consumer's trigger record, so replaying both would run the job twice.
func TestMigrationQueueDeadLetterSingleEvent(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-single-dlq@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-single-dlq", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding := seedLegacyQueueBindingConsumer(t, pool, account.ID, app.ID, "jobs", true)
	triggerID := binding.Changes[0].TriggerID
	var invocation string
	if err := pool.QueryRow(ctx, `insert into invocations(account_id,app_id,source,queue_name,state,attempts,payload)
values($1,$2,'queue','jobs','dispatching',3,'{"job_id":"r2-121"}') returning id`, account.ID, app.ID).Scan(&invocation); err != nil {
		t.Fatal(err)
	}
	record, err := store.InsertTriggerRecord(ctx, triggerID, invocation, []byte(`{"job_id":"r2-121"}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}

	// The queue poller's terminal Nack and the dispatcher's DLQ write.
	if _, err := pool.Exec(ctx, `update invocations set state='dead_letter', last_error='max_attempts', completed_at=now() where id=$1`, invocation); err != nil {
		t.Fatal(err)
	}
	if err := store.InsertTriggerDeadLetter(ctx, record, triggerID, "max_attempts", "drop", []byte(`"function state=failed"`)); err != nil {
		t.Fatal(err)
	}

	rows, err := pool.Query(ctx, `select source, source_id::text from dead_letter_events where app_id=$1 order by source`, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var events []string
	for rows.Next() {
		var source, id string
		if err := rows.Scan(&source, &id); err != nil {
			t.Fatal(err)
		}
		events = append(events, source+":"+id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != "invocation:"+invocation {
		t.Fatalf("dead-letter events = %v, want only the invocation %s", events, invocation)
	}
}
