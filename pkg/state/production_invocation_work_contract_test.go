// adr: 583
package state_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemProductionQueueBoundary(t *testing.T) {
	testProductionQueueBoundary(t, state.NewMemStore())
}

func testProductionQueueBoundary(t *testing.T, store environmentQueueInvocationTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	// A backlog in the same app and logical queue must be filtered before LIMIT.
	var stages []state.Invocation
	for range 24 {
		stages = append(stages, enqueueStageQueue(t.Context(), t, store, f))
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "other", 0, f.spec.Settings.QueueBindings.Bindings); err != nil {
		t.Fatal(err)
	}
	otherDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, otherDep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, otherDep.ID); err != nil {
		t.Fatal(err)
	}
	sibling := f
	sibling.dep = otherDep
	stages = append(stages, enqueueStageQueue(t.Context(), t, store, sibling))
	enqueue := func(source state.InvocationSource, queueName string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID,
			Source: source, QueueName: queueName, DueAt: time.Now(), Payload: []byte(`{"production":true}`)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	first, second := enqueue(state.InvocationQueue, "orders"), enqueue(state.InvocationQueue, "orders")
	enqueue(state.InvocationQueue, "")
	active := enqueue(state.InvocationQueue, "orders")
	if _, err := store.ClaimInvocation(ctx, active.ID, "", 300); err != nil {
		t.Fatal(err)
	}
	dead := enqueue(state.InvocationQueue, "orders")
	if _, err := store.ClaimInvocation(ctx, dead.ID, "", 300); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, dead.ID, "production failure", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	stages = append(stages, enqueueStageQueue(t.Context(), t, store, f))
	stageDead := stages[len(stages)-1]
	if _, err := store.ClaimInvocationWithCap(ctx, stageDead.ID, "", 300, 5); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, stageDead.ID, "private stage failure", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, stages[0].ID, "", 300, 5); err != nil {
		t.Fatal(err)
	}
	ordinary := enqueue(state.InvocationAsyncInvoke, "")
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "foreign-queue", WorkloadName: "foreign-queue"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: otherApp.ID, AccountID: f.account.ID, Source: state.InvocationQueue, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	before := make([]state.Invocation, len(stages))
	proofs := make([]state.InvocationEnvironmentQueueAdmission, len(stages))
	for i, inv := range stages {
		before[i], err = store.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		proofs[i], err = store.InvocationEnvironmentQueueAdmission(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, quotaBefore, err := store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := store.QueueState(ctx, f.app.ID)
	if err != nil || stats.Depth != 4 || stats.InFlight != 1 || stats.DeadLetter != 1 || !stats.OldestPendingAt.Equal(first.CreatedAt) {
		t.Fatalf("production state = %+v %v", stats, err)
	}
	stats, err = store.QueueStateForQueue(ctx, f.app.ID, "orders")
	if err != nil || stats.Depth != 3 || stats.InFlight != 1 || stats.DeadLetter != 1 {
		t.Fatalf("named state = %+v %v", stats, err)
	}
	stats, err = store.QueueStateForQueue(ctx, f.app.ID, "")
	if err != nil || stats.Depth != 1 || stats.InFlight != 0 || stats.DeadLetter != 0 {
		t.Fatalf("legacy queue state = %+v %v", stats, err)
	}
	if count, err := store.CountPendingInvocations(ctx, f.app.ID, state.InvocationQueue); err != nil || count != 4 {
		t.Fatalf("count=%d %v", count, err)
	}
	page, err := store.QueuePeek(ctx, f.app.ID, 1, "")
	if err != nil || len(page) != 1 || page[0].ID != first.ID {
		t.Fatalf("first peek=%+v %v", page, err)
	}
	page[0].Payload[0] = '!'
	page, err = store.QueuePeek(ctx, f.app.ID, 1, first.ID)
	if err != nil || len(page) != 1 || page[0].ID != second.ID {
		t.Fatalf("next peek=%+v %v", page, err)
	}
	if row, err := store.ProductionQueueInvocationByID(ctx, first.ID); err != nil || !environmentQueueJSONEqual(row.Payload, []byte(`{"production":true}`)) {
		t.Fatalf("read mutated payload=%+v %v", row, err)
	}
	letters, err := store.QueueDeadLetter(ctx, f.app.ID, 1, "")
	if err != nil || len(letters) != 1 || letters[0].ID != dead.ID {
		t.Fatalf("production letters=%+v %v", letters, err)
	}
	for _, cursor := range []string{stages[1].ID, stageDead.ID, ordinary.ID, foreign.ID, uuid.NewString()} {
		for _, read := range []func() ([]state.Invocation, error){
			func() ([]state.Invocation, error) { return store.QueuePeek(ctx, f.app.ID, 1, cursor) },
			func() ([]state.Invocation, error) { return store.QueueDeadLetter(ctx, f.app.ID, 1, cursor) },
		} {
			if rows, err := read(); err != nil || len(rows) != 0 {
				t.Fatalf("foreign cursor %s: %+v %v", cursor, rows, err)
			}
		}
	}
	for _, inv := range stages {
		if _, err := store.ProductionQueueInvocationByID(ctx, inv.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("stage read: %v", err)
		}
	}
	if _, err := store.RetryQueueDeadLetter(ctx, f.account.ID, stageDead.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stage retry: %v", err)
	}
	for _, read := range []func() ([]state.DeadLetterEvent, error){
		func() ([]state.DeadLetterEvent, error) { return store.ListDeadLetterEvents(ctx, f.app.ID, 1, "") },
		func() ([]state.DeadLetterEvent, error) {
			return store.ListDeadLetterEventsForAccount(ctx, f.account.ID, 1, "")
		},
	} {
		if rows, err := read(); err != nil || len(rows) != 1 || rows[0].SourceID != dead.ID {
			t.Fatalf("unified production DLQ=%+v %v", rows, err)
		}
	}
	for i, inv := range stages {
		row, err := store.InvocationByID(ctx, inv.ID)
		if err != nil || !reflect.DeepEqual(row, before[i]) {
			t.Fatalf("stage row changed: %+v %v", row, err)
		}
		proof, err := store.InvocationEnvironmentQueueAdmission(ctx, inv.ID)
		if err != nil || !reflect.DeepEqual(proof, proofs[i]) {
			t.Fatalf("stage proof changed: %+v %v", proof, err)
		}
	}
	_, quotaAfter, err := store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || quotaAfter != quotaBefore {
		t.Fatalf("queue reads leaked quota: %d %d %v", quotaBefore, quotaAfter, err)
	}
	foreignAccount, err := store.CreateAccount(ctx, "foreign-cursor-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	foreignOwnerApp, err := store.CreateApp(ctx, state.App{AccountID: foreignAccount.ID, Slug: "foreign-cursor"})
	if err != nil {
		t.Fatal(err)
	}
	foreignDead, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: foreignAccount.ID, AppID: foreignOwnerApp.ID, Source: state.InvocationQueue, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocation(ctx, foreignDead.ID, "", 300); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, foreignDead.ID, "foreign", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	foreignEvents, err := store.ListDeadLetterEventsForAccount(ctx, foreignAccount.ID, 1, "")
	if err != nil || len(foreignEvents) != 1 {
		t.Fatalf("foreign ledger=%+v %v", foreignEvents, err)
	}
	for _, read := range []func() ([]state.DeadLetterEvent, error){
		func() ([]state.DeadLetterEvent, error) {
			return store.ListDeadLetterEvents(ctx, f.app.ID, 1, foreignEvents[0].ID)
		},
		func() ([]state.DeadLetterEvent, error) {
			return store.ListDeadLetterEventsForAccount(ctx, f.account.ID, 1, foreignEvents[0].ID)
		},
	} {
		if rows, err := read(); err != nil || len(rows) != 0 {
			t.Fatalf("foreign unified cursor=%+v %v", rows, err)
		}
	}
	// Both bulk operator verbs must skip stage failures before applying limits.
	if n, err := store.ReplayDeadLetterEventsForAccount(ctx, f.account.ID, 1); err != nil || n != 1 {
		t.Fatalf("bulk replay=%d %v", n, err)
	}
	if row, err := store.InvocationByID(ctx, dead.ID); err != nil || row.State != state.InvocationPending || row.LastReplayedAt == nil {
		t.Fatalf("production replay=%+v %v", row, err)
	}
	if n, err := store.DeleteDeadLetterEvents(ctx, f.account.ID, f.app.ID, 1); err != nil || n != 1 {
		t.Fatalf("bulk discard=%d %v", n, err)
	}
	if row, err := store.InvocationByID(ctx, stageDead.ID); err != nil || !reflect.DeepEqual(row, before[len(before)-1]) {
		t.Fatalf("bulk actions changed stage: %+v %v", row, err)
	}
}
