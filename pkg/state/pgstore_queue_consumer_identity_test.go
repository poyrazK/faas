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
	claimed, err := store.ClaimQueueTriggerInvocation(ctx, current.ID, triggerID, app.ID, name, 60)
	if err != nil || claimed.State != state.InvocationDispatching || claimed.Attempts != 1 {
		t.Fatalf("live binding claim: state=%s attempts=%d err=%v", claimed.State, claimed.Attempts, err)
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
