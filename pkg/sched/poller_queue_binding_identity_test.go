// adr: 568 — environment intent and runtime ownership contracts.
package sched

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueuePollerBindingIdentitySurvivesRename(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-rename-poller@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-rename-poller", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	original, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "original", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	oldTrigger, err := store.TriggerByID(ctx, original.Changes[0].TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := newQueuePoller(pool, oldTrigger, nil)
	if err != nil {
		t.Fatal(err)
	}
	poller := source.(*queuePoller)
	enqueue := func(name string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: name, DueAt: time.Now().Add(-time.Second), Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	old, unnamed := enqueue("orders"), enqueue("")
	name := "payments"
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name}); err != nil {
		t.Fatal(err)
	}
	trigger, err := store.TriggerByID(ctx, oldTrigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "replacement", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	replacementWork, fresh := enqueue("orders"), enqueue("payments")
	if rows, err := store.ListDueInvocations(ctx, time.Now(), 100); err != nil || len(rows) != 0 {
		t.Fatalf("bound unnamed work leaked to generic drain: %d %v", len(rows), err)
	}
	if result := poller.Poll(ctx, oldTrigger); result.Error != nil || len(result.Records) != 0 {
		t.Fatalf("cached name claimed work=%+v", result)
	}
	first := poller.Poll(ctx, trigger)
	if first.Error != nil || len(first.Records) != 1 || first.Records[0].InvocationID != old.ID {
		t.Fatalf("renamed consumer lost original backlog=%+v", first)
	}
	if blocked := poller.Poll(ctx, trigger); blocked.Error != nil || len(blocked.Records) != 0 {
		t.Fatalf("old label escaped binding cap=%+v", blocked)
	}
	// A partial batch rollback made with a cached name must release its own UID
	// claim after a rename, without releasing another dispatch attempt.
	if err := poller.releaseNamedClaims(ctx, map[string]queueDeliveryClaim{old.ID: {Attempt: 1}}, oldTrigger); err != nil {
		t.Fatal(err)
	}
	row, err := store.InvocationByID(ctx, old.ID)
	if err != nil || row.State != state.InvocationPending || row.QueueBindingID != original.Binding.ID || row.QueueName != "orders" {
		t.Fatalf("release changed admission=%+v %v", row, err)
	}
	retried := poller.Poll(ctx, trigger)
	if retried.Error != nil || len(retried.Records) != 1 || retried.Records[0].InvocationAttempt != 2 {
		t.Fatalf("released work lost attempt fence=%+v", retried)
	}
	if err := poller.releaseNamedClaims(ctx, map[string]queueDeliveryClaim{old.ID: {Attempt: 1}}, oldTrigger); err != nil {
		t.Fatal(err)
	}
	if row, err := store.InvocationByID(ctx, old.ID); err != nil || row.State != state.InvocationDispatching || row.Attempts != 2 {
		t.Fatalf("stale release reclaimed newer attempt=%+v %v", row, err)
	}
	if err := poller.Ack(ctx, trigger, []string{old.ID}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []state.Invocation{unnamed, fresh} {
		result := poller.Poll(ctx, trigger)
		if result.Error != nil || len(result.Records) != 1 || result.Records[0].InvocationID != expected.ID {
			t.Fatalf("binding identity routing=%+v want=%s", result, expected.ID)
		}
		if err := poller.Ack(ctx, trigger, []string{expected.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if result := poller.Poll(ctx, trigger); result.Error != nil || len(result.Records) != 0 {
		t.Fatalf("renamed consumer stole replacement=%+v", result)
	}
	if row, err := store.InvocationByID(ctx, replacementWork.ID); err != nil || row.State != state.InvocationPending || row.Attempts != 0 {
		t.Fatalf("replacement changed=%+v %v", row, err)
	}
	disabled, enabled := false, true
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, original.Binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, replacement.Binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	replacementTrigger, err := store.TriggerByID(ctx, replacement.Changes[0].TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	replacementSource, err := newQueuePoller(pool, replacementTrigger, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := replacementSource.Poll(ctx, replacementTrigger)
	if result.Error != nil || len(result.Records) != 1 || result.Records[0].InvocationID != replacementWork.ID {
		t.Fatalf("replacement routing=%+v", result)
	}
}
