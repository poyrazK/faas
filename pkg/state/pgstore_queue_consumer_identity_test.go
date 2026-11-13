//go:build !no_pg

package state_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgQueueConsumerClaimsRequireLiveBinding(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "queue-claim-identity@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-claim-identity", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID,
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	triggerID := created.Changes[0].TriggerID
	enqueue := func(queue string) state.Invocation {
		t.Helper()
		row, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID,
			Source: state.InvocationQueue, QueueName: queue, DueAt: time.Now().Add(-time.Second), Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	old := enqueue("jobs")
	disabled := false
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, old.ID, triggerID, app.ID, "jobs", 60); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("disabled consumer claimed: %v", err)
	}
	enabled, name := true, "payments"
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled, QueueName: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, old.ID, triggerID, app.ID, "jobs", 60); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("renamed cached consumer claimed: %v", err)
	}
	if row, err := store.InvocationByID(ctx, old.ID); err != nil || row.State != state.InvocationPending || row.Attempts != 0 {
		t.Fatalf("stale consumer changed invocation: state=%s attempts=%d err=%v", row.State, row.Attempts, err)
	}
	current := enqueue("payments")
	claimed, err := store.ClaimQueueTriggerInvocation(ctx, old.ID, triggerID, app.ID, name, 60)
	if err != nil || claimed.State != state.InvocationDispatching || claimed.Attempts != 1 {
		t.Fatalf("live binding claim: state=%s attempts=%d err=%v", claimed.State, claimed.Attempts, err)
	}
	if claimed.QueueBindingID != created.Binding.ID || claimed.QueueName != "jobs" {
		t.Fatal("rename rewrote admission identity")
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, current.ID, triggerID, app.ID, name, 60); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("renamed backlog bypassed cap: %v", err)
	}
	pending := enqueue("payments")
	pull := "pull"
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Mode: &pull}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimQueueTriggerInvocation(ctx, pending.ID, triggerID, app.ID, name, 60); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("removed consumer claimed: %v", err)
	}
}

// Hunt 33 H33-27: concurrent `queue send` deadlocked against the queue
// poller. A capped enqueue locks the app FOR UPDATE and then, in the
// invocation insert trigger, the binding FOR SHARE; the claim locked the
// binding FOR UPDATE and then reached the app through the invocation update
// triggers. The claim now takes the app lock first, so while an enqueue
// holds the app the claim waits without holding the binding.
func TestPgQueueClaimLocksAppBeforeBinding(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "queue-claim-lock-order@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-claim-lock-order", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID,
		Name: "jobs", QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID,
		Source: state.InvocationQueue, QueueName: "jobs", DueAt: time.Now().Add(-time.Second), Payload: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}

	enqueue, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = enqueue.Rollback(ctx) }()
	if _, err := enqueue.Exec(ctx, `select id from apps where id = $1 for update`, app.ID); err != nil {
		t.Fatal(err)
	}
	claimed := make(chan error, 1)
	go func() {
		_, err := store.ClaimQueueTriggerInvocation(ctx, inv.ID, created.Changes[0].TriggerID, app.ID, "jobs", 60)
		claimed <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `select count(*) from pg_stat_activity
			where datname = current_database() and wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("claim never waited on the app lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := enqueue.Exec(ctx, `set local lock_timeout = '2s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := enqueue.Exec(ctx, `select id from queue_bindings where id = $1 for share`, created.Binding.ID); err != nil {
		t.Fatalf("enqueue could not take the binding share lock while the claim waited: %v", err)
	}
	if err := enqueue.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-claimed; err != nil {
		t.Fatalf("claim after the enqueue committed: %v", err)
	}
}
