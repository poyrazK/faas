package state_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestMemKeyedInvocationLifecycle(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "keyed-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), Slug: "keyed-test", AccountID: account.ID, RAMMB: 256, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	assertKeyedInvocationLifecycle(t, ctx, store, app.ID, account.ID)
}

func TestPgKeyedInvocationLifecycle(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertKeyedInvocationLifecycle(t, ctx, store, appID, accountID)
}

func TestMemPendingQueueWorkInLane(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "queue-lane-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), Slug: "queue-lane", AccountID: account.ID, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	assertPendingQueueWorkInLane(t, ctx, store, app.ID, account.ID)
}

func TestPgPendingQueueWorkInLane(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertPendingQueueWorkInLane(t, ctx, store, appID, accountID)
}

func assertPendingQueueWorkInLane(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
	t.Helper()
	policy := workpolicy.Policy{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	queue, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: accountID, Source: state.InvocationQueue,
		DueAt: time.Now().Add(time.Hour),
	}, policy, "s:doc-1")
	if err != nil {
		t.Fatal(err)
	}
	count, err := store.(state.PendingQueueWorkCounter).PendingQueueWorkInLane(ctx, appID, policy.Name, queue.WorkKeyDigest)
	if err != nil || count != 1 {
		t.Fatalf("pending queue lane count = %d, err=%v", count, err)
	}
	if _, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
		AppID: appID, AccountID: accountID, Source: state.InvocationAsyncInvoke,
		DueAt: time.Now().Add(time.Hour),
	}, policy, "s:doc-1"); err != nil {
		t.Fatal(err)
	}
	count, err = store.(state.PendingQueueWorkCounter).PendingQueueWorkInLane(ctx, appID, policy.Name, queue.WorkKeyDigest)
	if err != nil || count != 0 {
		t.Fatalf("superseded queue lane count = %d, err=%v", count, err)
	}
}

func assertKeyedInvocationLifecycle(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
	t.Helper()
	all := workpolicy.Policy{Name: "order-updates", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}
	makeInvocation := func() state.Invocation {
		return state.Invocation{ID: uuid.NewString(), AppID: appID, AccountID: accountID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/work",
			DueAt: time.Now().Add(-time.Second)}
	}
	first, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), all, "s:order-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), all, "s:order-1")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), all, "s:order-2")
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkSequence != 1 || second.WorkSequence != 2 || other.WorkSequence != 1 {
		t.Fatalf("lane sequences = %d, %d, %d", first.WorkSequence, second.WorkSequence, other.WorkSequence)
	}
	pager, ok := store.(interface {
		ListDueInvocationsAfter(context.Context, time.Time, state.InvocationDueCursor, int) ([]state.Invocation, error)
	})
	if !ok {
		t.Fatal("store does not implement the scheduler's due cursor")
	}
	due, err := pager.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64)
	if err != nil {
		t.Fatal(err)
	}
	dueIDs := map[string]bool{}
	for _, row := range due {
		dueIDs[row.ID] = true
	}
	if !dueIDs[first.ID] || !dueIDs[other.ID] || dueIDs[second.ID] {
		t.Fatalf("due scan should expose one head per key: %+v", dueIDs)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, second.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("later same-key claim = %v, want conflict", err)
	}
	// Simultaneous schedulers can claim only the oldest row of this lane.
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan struct {
		id  string
		err error
	}, 2)
	for _, id := range []string{first.ID, second.ID} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, err := store.ClaimInvocationWithCap(ctx, id, "", 30, 10)
			results <- struct {
				id  string
				err error
			}{id, err}
		}(id)
	}
	close(start)
	wg.Wait()
	close(results)
	for result := range results {
		if result.id == first.ID && result.err != nil {
			t.Fatalf("oldest claim failed: %v", result.err)
		}
		if result.id == second.ID && !errors.Is(result.err, state.ErrConflict) {
			t.Fatalf("later claim = %v, want conflict", result.err)
		}
	}
	if _, err := store.ClaimInvocationWithCap(ctx, other.ID, "", 30, 10); err != nil {
		t.Fatalf("different key could not run: %v", err)
	}
	if err := store.CompleteKeyedInvocation(ctx, other.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteInvocation(ctx, first.ID, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unfenced completion = %v", err)
	}
	if err := store.CancelInvocation(ctx, first.ID); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("running keyed cancellation = %v, want conflict", err)
	}
	if err := store.FailInvocation(ctx, first.ID, "retry", time.Millisecond, 0, state.WithClaimAttempt(1)); err != nil {
		t.Fatal(err)
	}
	if err := store.FailInvocation(ctx, first.ID, "stale", 0, 0, state.WithClaimAttempt(1)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale failure during pending = %v", err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, second.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retry must keep FIFO position: %v", err)
	}
	time.Sleep(3 * time.Millisecond)
	reclaimed, err := store.ClaimInvocationWithCap(ctx, first.ID, "", 30, 10)
	if err != nil || reclaimed.Attempts != 2 {
		t.Fatalf("retry claim = %+v, %v", reclaimed, err)
	}
	if err := store.CompleteKeyedInvocation(ctx, first.ID, 1, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale completion = %v", err)
	}
	if err := store.FailInvocation(ctx, first.ID, "stale", 0, 0, state.WithClaimAttempt(1)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("stale failure after new claim = %v", err)
	}
	if err := store.CompleteKeyedInvocation(ctx, first.ID, 2, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, second.ID, "", 30, 10); err != nil {
		t.Fatalf("next row did not advance: %v", err)
	}
	if err := store.CompleteKeyedInvocation(ctx, second.ID, 1, nil); err != nil {
		t.Fatal(err)
	}

	latest := workpolicy.Policy{Name: "document-index", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	old, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), latest, "s:doc-1")
	if err != nil {
		t.Fatal(err)
	}
	newest, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), latest, "s:doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.InvocationByID(ctx, old.ID); got.State != state.InvocationSuperseded {
		t.Fatalf("older pending state = %s", got.State)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, newest.ID, "", 30, 10); err != nil {
		t.Fatal(err)
	}
	pending, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), latest, "s:doc-1")
	if err != nil {
		t.Fatal(err)
	}
	final, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), latest, "s:doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := store.InvocationByID(ctx, newest.ID); got.State != state.InvocationDispatching {
		t.Fatalf("running row was replaced: %s", got.State)
	}
	if got, _ := store.InvocationByID(ctx, pending.ID); got.State != state.InvocationSuperseded {
		t.Fatalf("pending row was not superseded: %s", got.State)
	}
	replayed, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{ID: pending.ID, AppID: appID, AccountID: accountID}, latest, "s:doc-1")
	if err != nil || replayed.State != state.InvocationSuperseded {
		t.Fatalf("replayed id = %+v, %v", replayed, err)
	}
	if got, _ := store.InvocationByID(ctx, final.ID); got.State != state.InvocationPending {
		t.Fatalf("replay superseded newer row: %s", got.State)
	}
	if err := store.CompleteKeyedInvocation(ctx, newest.ID, 1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, final.ID, "", 30, 10); err != nil {
		t.Fatalf("latest did not advance: %v", err)
	}
	debounced := workpolicy.Policy{Name: "debounced", MaxRunningPerKey: 1, Debounce: time.Hour, ExpiresAfter: 2 * time.Hour}
	delayed, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), debounced, "s:quiet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, delayed.ID, "", 30, 10); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("debounced claim = %v, want conflict", err)
	}
	expiring := workpolicy.Policy{Name: "expiring", MaxRunningPerKey: 1, ExpiresAfter: time.Millisecond}
	expired, err := store.EnqueueKeyedInvocation(ctx, makeInvocation(), expiring, "s:old")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := store.ClaimInvocationWithCap(ctx, expired.ID, "", 30, 10); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired claim = %v, want not found", err)
	}
	if got, _ := store.InvocationByID(ctx, expired.ID); got.State != state.InvocationExpired ||
		got.Outcome == nil || *got.Outcome != state.OutcomeExpired {
		t.Fatalf("expiry state/outcome = %s/%v", got.State, got.Outcome)
	}
	future := makeInvocation()
	future.DueAt = time.Now().Add(time.Hour)
	neverDue, err := store.EnqueueKeyedInvocation(ctx, future, expiring, "s:never-due")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	expirer, ok := store.(interface {
		ExpirePendingKeyedInvocations(context.Context, time.Time, int) (int, error)
	})
	if !ok {
		t.Fatal("store does not implement keyed pending expiry")
	}
	if count, err := expirer.ExpirePendingKeyedInvocations(ctx, time.Now(), 64); err != nil || count != 1 {
		t.Fatalf("expiry sweep = %d, %v", count, err)
	}
	if got, _ := store.InvocationByID(ctx, neverDue.ID); got.State != state.InvocationExpired {
		t.Fatalf("future due row was not expired: %s", got.State)
	}
}
