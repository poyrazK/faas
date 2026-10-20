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

func newAdmissionDepthMem(t *testing.T) (state.Store, context.Context, string, string) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "depth-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), Slug: "depth-test", AccountID: account.ID, RAMMB: 256, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	return store, ctx, app.ID, account.ID
}

func TestMemInvocationAdmissionDepth(t *testing.T) {
	store, ctx, appID, accountID := newAdmissionDepthMem(t)
	assertInvocationAdmissionDepth(t, ctx, store, appID, accountID)
}

func TestPgInvocationAdmissionDepth(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertInvocationAdmissionDepth(t, ctx, store, appID, accountID)
}

func TestMemInvocationAdmissionDepthUnderContention(t *testing.T) {
	store, ctx, appID, accountID := newAdmissionDepthMem(t)
	assertInvocationAdmissionDepthUnderContention(t, ctx, store, appID, accountID)
}

// The apid queue-depth check used to count, then insert in a separate
// statement, so concurrent senders all saw room and overshot the plan cap.
func TestPgInvocationAdmissionDepthUnderContention(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertInvocationAdmissionDepthUnderContention(t, ctx, store, appID, accountID)
}

func assertInvocationAdmissionDepth(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
	t.Helper()
	const limit = 2
	latest := workpolicy.Policy{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	capped := func(source state.InvocationSource) state.Invocation {
		return state.Invocation{ID: uuid.NewString(), AppID: appID, AccountID: accountID, Source: source,
			DueAt: time.Now().Add(time.Hour), AdmissionMaxPending: limit}
	}
	wantDepthError := func(t *testing.T, err error) {
		t.Helper()
		var depth *state.InvocationDepthError
		if !errors.As(err, &depth) || !errors.Is(err, state.ErrQuotaExceeded) {
			t.Fatalf("err = %v, want InvocationDepthError wrapping ErrQuotaExceeded", err)
		}
		if depth.Limit != limit || depth.Observed != limit {
			t.Fatalf("depth error = %+v, want limit and observed %d", depth, limit)
		}
	}

	if _, err := store.EnqueueKeyedInvocation(ctx, capped(state.InvocationQueue), latest, "s:doc"); err != nil {
		t.Fatalf("first keyed send: %v", err)
	}
	if _, err := store.EnqueueInvocation(ctx, capped(state.InvocationQueue)); err != nil {
		t.Fatalf("plain send below cap: %v", err)
	}
	_, err := store.EnqueueInvocation(ctx, capped(state.InvocationQueue))
	wantDepthError(t, err)

	// keep_latest replaces the lane's pending row, so it is admitted at the
	// cap; a new lane would add work and is refused.
	replacement := capped(state.InvocationQueue)
	if _, err := store.EnqueueKeyedInvocation(ctx, replacement, latest, "s:doc"); err != nil {
		t.Fatalf("keep_latest replacement at cap: %v", err)
	}
	_, err = store.EnqueueKeyedInvocation(ctx, capped(state.InvocationQueue), latest, "s:other")
	wantDepthError(t, err)
	// A producer retrying an admitted ID gets its row back, not a refusal.
	if replay, err := store.EnqueueKeyedInvocation(ctx, replacement, latest, "s:doc"); err != nil || replay.ID != replacement.ID {
		t.Fatalf("idempotent keyed replay at cap = %+v, %v", replay, err)
	}

	if n, err := store.CountPendingInvocations(ctx, appID, state.InvocationQueue); err != nil || n != limit {
		t.Fatalf("pending queue rows = %d, %v; want %d", n, err, limit)
	}
	// The cap is per producer and source: uncapped producers and other
	// sources are unaffected.
	uncapped := capped(state.InvocationQueue)
	uncapped.AdmissionMaxPending = 0
	if _, err := store.EnqueueInvocation(ctx, uncapped); err != nil {
		t.Fatalf("uncapped send: %v", err)
	}
	if _, err := store.EnqueueInvocation(ctx, capped(state.InvocationDelayedTask)); err != nil {
		t.Fatalf("delayed task counted against the queue cap: %v", err)
	}
}

func assertInvocationAdmissionDepthUnderContention(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
	t.Helper()
	const contenders, limit = 24, 5
	start := make(chan struct{})
	var mu sync.Mutex
	admitted, refused := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
				Source: state.InvocationQueue, DueAt: time.Now().Add(time.Hour), AdmissionMaxPending: limit})
			mu.Lock()
			defer mu.Unlock()
			var depth *state.InvocationDepthError
			switch {
			case err == nil:
				admitted++
			case errors.As(err, &depth):
				refused++
			default:
				t.Errorf("enqueue: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if admitted != limit || refused != contenders-limit {
		t.Fatalf("admitted %d, refused %d; want %d and %d", admitted, refused, limit, contenders-limit)
	}
	if n, err := store.CountPendingInvocations(ctx, appID, state.InvocationQueue); err != nil || n != limit {
		t.Fatalf("pending queue rows = %d, %v; want %d", n, err, limit)
	}
}
