package state_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func newDefaultRetentionMem(t *testing.T) (state.Store, context.Context, string, string) {
	t.Helper()
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "retention-"+uuid.NewString()+"@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), Slug: "retention-test", AccountID: account.ID, RAMMB: 256, Runtime: "node22"})
	if err != nil {
		t.Fatal(err)
	}
	return store, ctx, app.ID, account.ID
}

func TestMemStampDefaultInvocationRetention(t *testing.T) {
	store, ctx, appID, accountID := newDefaultRetentionMem(t)
	assertStampDefaultInvocationRetention(t, ctx, store, appID, accountID)
}

// ADR 20261019104819: rows from producers that set no horizon (queue sends,
// crons, event deliveries, ...) were never reaped. They now get the plan
// default, with a 30-day floor for failures that DLQ replay still needs.
func TestPgStampDefaultInvocationRetention(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertStampDefaultInvocationRetention(t, ctx, store, appID, accountID)
}

func TestMemInvocationReaperTerminalStates(t *testing.T) {
	store, ctx, appID, accountID := newDefaultRetentionMem(t)
	assertInvocationReaperTerminalStates(t, ctx, store, appID, accountID)
}

func TestPgInvocationReaperTerminalStates(t *testing.T) {
	store, ctx, appID, accountID := seedInvocationPg(t)
	assertInvocationReaperTerminalStates(t, ctx, store, appID, accountID)
}

func assertStampDefaultInvocationRetention(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
	t.Helper()
	stamper, ok := store.(state.InvocationDefaultRetentionStore)
	if !ok {
		t.Fatalf("%T does not stamp default retention", store)
	}
	account, err := store.AccountByID(ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	planDefault := time.Duration(api.MustLimitsFor(account.Plan).MaxAsyncResultRetentionSeconds) * time.Second
	failureRetention := max(planDefault, state.InvocationRetentionFailureFloor)
	enqueue := func(retention *time.Time) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
			Source: state.InvocationQueue, Method: "POST", Path: "/x", Payload: json.RawMessage(`{}`),
			DueAt: time.Now().UTC(), ResultRetentionUntil: retention})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
			t.Fatal(err)
		}
		return inv
	}

	completed := enqueue(nil)
	if err := store.CompleteInvocation(ctx, completed.ID, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	dead := enqueue(nil)
	if err := store.FailInvocation(ctx, dead.ID, "boom", time.Second, 1); err != nil {
		t.Fatal(err)
	}
	explicitUntil := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	explicit := enqueue(&explicitUntil)
	if err := store.CompleteInvocation(ctx, explicit.ID, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	pending, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
		Source: state.InvocationQueue, Method: "POST", Path: "/live", DueAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	stamped, err := stamper.StampDefaultInvocationRetention(ctx, 100)
	if err != nil || stamped != 2 {
		t.Fatalf("stamped = %d, %v; want the completed and dead-lettered rows", stamped, err)
	}
	if again, err := stamper.StampDefaultInvocationRetention(ctx, 100); err != nil || again != 0 {
		t.Fatalf("second stamp = %d, %v; want 0", again, err)
	}

	wantHorizon := func(id string, retention time.Duration) {
		t.Helper()
		inv, err := store.InvocationByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if inv.ResultRetentionUntil == nil {
			t.Fatalf("%s: no retention horizon stamped", inv.State)
		}
		settled := inv.CreatedAt
		if inv.CompletedAt != nil {
			settled = *inv.CompletedAt
		}
		if got := inv.ResultRetentionUntil.Sub(settled); got < retention-time.Second || got > retention+time.Second {
			t.Fatalf("%s: horizon = settled + %v, want + %v", inv.State, got, retention)
		}
	}
	wantHorizon(completed.ID, planDefault)
	wantHorizon(dead.ID, failureRetention)
	if inv, err := store.InvocationByID(ctx, explicit.ID); err != nil || inv.ResultRetentionUntil == nil || !inv.ResultRetentionUntil.Equal(explicitUntil) {
		t.Fatalf("explicit retention changed: %v, %v", inv.ResultRetentionUntil, err)
	}
	if inv, err := store.InvocationByID(ctx, pending.ID); err != nil || inv.ResultRetentionUntil != nil {
		t.Fatalf("pending row stamped: %v, %v", inv.ResultRetentionUntil, err)
	}

	// Past the plan default only the completed row is due; the failure floor
	// keeps the dead-lettered row for DLQ replay.
	due, err := store.ListExpiredInvocationsForReaper(ctx, time.Now().UTC().Add(planDefault+time.Minute), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(due, completed.ID) || slices.Contains(due, pending.ID) {
		t.Fatalf("due after plan default = %v; want completed %s, not pending %s", due, completed.ID, pending.ID)
	}
	if failureRetention > planDefault && slices.Contains(due, dead.ID) {
		t.Fatalf("dead-lettered row due before the failure floor: %v", due)
	}
}

func assertInvocationReaperTerminalStates(t *testing.T, ctx context.Context, store state.Store, appID, accountID string) {
	t.Helper()
	past := time.Now().UTC().Add(-time.Hour)
	latest := workpolicy.Policy{Name: "latest", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	keyed := func() state.Invocation {
		t.Helper()
		inv, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{AppID: appID, AccountID: accountID,
			Source: state.InvocationAsyncInvoke, Method: "POST", Path: "/k", DueAt: time.Now().UTC().Add(time.Hour),
			ResultRetentionUntil: &past}, latest, "s:doc")
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	superseded := keyed()
	live := keyed()
	if inv, err := store.InvocationByID(ctx, superseded.ID); err != nil || inv.State != state.InvocationSuperseded {
		t.Fatalf("first keyed row = %s, %v; want superseded", inv.State, err)
	}
	due, err := store.ListExpiredInvocationsForReaper(ctx, time.Now().UTC(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(due, superseded.ID) {
		t.Fatalf("superseded row with a past horizon not due: %v", due)
	}
	if slices.Contains(due, live.ID) {
		t.Fatalf("pending row with a past horizon listed for deletion: %v", due)
	}
}
