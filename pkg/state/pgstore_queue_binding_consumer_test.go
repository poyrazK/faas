//go:build !no_pg

package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQueueConsumerRetirementFailureRestoresConsumerAndReceipts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-delete-rollback@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-delete-rollback", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs",
		Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	triggerID := created.Changes[0].TriggerID
	recordID, err := store.InsertTriggerRecord(ctx, triggerID, "receipt", []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	// Consumer disable happens before this injected retirement failure.
	// PostgreSQL must restore enabled intent and keep the receipt.
	if _, err := pool.Exec(ctx, `create function reject_queue_binding_delete() returns trigger language plpgsql as $$
	begin raise exception 'injected binding deletion failure'; end $$;
	create trigger reject_queue_binding_delete before update of retired_at on queue_bindings
	for each row execute function reject_queue_binding_delete()`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DeleteQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID); err == nil {
		t.Fatal("injected deletion failure accepted")
	}
	if _, err := store.QueueBindingByID(ctx, account.ID, app.ID, created.Binding.ID); err != nil {
		t.Fatalf("failed deletion lost binding: %v", err)
	}
	if trigger, err := store.TriggerByID(ctx, triggerID); err != nil || !trigger.Enabled {
		t.Fatalf("failed retirement changed consumer: enabled=%t err=%v", trigger.Enabled, err)
	}
	if got, err := store.TriggerRecordIDByItemIdentifier(ctx, triggerID, "receipt"); err != nil || got != recordID {
		t.Fatalf("failed deletion lost receipt: id=%q err=%v", got, err)
	}
}
