// adr: 20261022105723
package state_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemQueueBacklogBytesCap(t *testing.T) {
	testQueueBacklogBytesCap(t, state.NewMemStore())
}

func TestMemQueueMessageExpiry(t *testing.T) {
	testQueueMessageExpiry(t, state.NewMemStore())
}

func seedQueueBacklogApp(t *testing.T, store state.Store, account state.Account) state.App {
	t.Helper()
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "queue-backlog-" + uuid.NewString()[:8],
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func seedQueueBacklogAccount(t *testing.T, store state.Store, plan api.Plan) state.Account {
	t.Helper()
	account, err := store.CreateAccount(t.Context(), "queue-backlog-"+uuid.NewString()+"@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	return account
}

func enqueueBacklogMessage(t *testing.T, store state.Store, app state.App, source state.InvocationSource, maxBytes int64) (state.Invocation, error) {
	t.Helper()
	return store.EnqueueInvocation(t.Context(), state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: source,
		Payload: json.RawMessage(`{"message":"` + uuid.NewString() + `"}`), DueAt: time.Now().UTC(), AdmissionMaxBacklogBytes: maxBytes})
}

// The byte cap covers the account's pending queue messages across its apps:
// a send is refused while they hold the cap, and admitted again once one
// settles. Other accounts and other sources are unaffected.
func testQueueBacklogBytesCap(t *testing.T, store state.Store) {
	ctx := t.Context()
	account := seedQueueBacklogAccount(t, store, api.PlanPro)
	app, sibling := seedQueueBacklogApp(t, store, account), seedQueueBacklogApp(t, store, account)
	foreign := seedQueueBacklogApp(t, store, seedQueueBacklogAccount(t, store, api.PlanPro))

	first, err := enqueueBacklogMessage(t, store, app, state.InvocationQueue, 1)
	if err != nil {
		t.Fatalf("first message under the cap: %v", err)
	}
	_, err = enqueueBacklogMessage(t, store, sibling, state.InvocationQueue, 1)
	var backlog *state.InvocationBacklogBytesError
	if !errors.As(err, &backlog) || !errors.Is(err, state.ErrQuotaExceeded) || backlog.Limit != 1 || backlog.Observed <= 0 {
		t.Fatalf("send to another app of a full account = %v, want InvocationBacklogBytesError", err)
	}
	if _, err := enqueueBacklogMessage(t, store, foreign, state.InvocationQueue, 1); err != nil {
		t.Fatalf("another account's send: %v", err)
	}
	if _, err := enqueueBacklogMessage(t, store, sibling, state.InvocationDelayedTask, 1); err != nil {
		t.Fatalf("a delayed task does not count queue bytes: %v", err)
	}
	if err := store.CancelInvocation(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := enqueueBacklogMessage(t, store, sibling, state.InvocationQueue, 1); err != nil {
		t.Fatalf("send after the backlog drained: %v", err)
	}
}

// A pending queue message past its plan's retention moves to the dead
// letter. A message a worker holds, a delayed task, and a message on a plan
// with longer retention stay as they are.
func testQueueMessageExpiry(t *testing.T, store state.Store) {
	ctx := t.Context()
	hobby := seedQueueBacklogApp(t, store, seedQueueBacklogAccount(t, store, api.PlanHobby))
	scale := seedQueueBacklogApp(t, store, seedQueueBacklogAccount(t, store, api.PlanScale))
	enqueue := func(app state.App, source state.InvocationSource) state.Invocation {
		t.Helper()
		inv, err := enqueueBacklogMessage(t, store, app, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	waiting, held, delayed, scaleWaiting := enqueue(hobby, state.InvocationQueue), enqueue(hobby, state.InvocationQueue),
		enqueue(hobby, state.InvocationDelayedTask), enqueue(scale, state.InvocationQueue)
	if _, err := store.ClaimInvocationWithCap(ctx, held.ID, "", 600, 10); err != nil {
		t.Fatal(err)
	}
	stateOf := func(inv state.Invocation) state.Invocation {
		t.Helper()
		got, err := store.InvocationByID(ctx, inv.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	n, err := store.(state.QueueMessageExpiryStore).ExpirePendingQueueMessages(ctx, time.Now().Add(25*time.Hour), 100)
	if err != nil || n != 1 {
		t.Fatalf("expiry a day later = %d, %v; want 1", n, err)
	}
	if got := stateOf(waiting); got.State != state.InvocationDeadLetter || !strings.Contains(got.LastError, "expired") {
		t.Fatalf("Hobby message waiting 25 h is %s (%q); want dead_letter", got.State, got.LastError)
	}
	for _, inv := range []state.Invocation{held, delayed, scaleWaiting} {
		if got := stateOf(inv); got.State == state.InvocationDeadLetter {
			t.Fatalf("%s %s expired; only pending queue messages past their plan's retention do", inv.Source, inv.ID)
		}
	}
	n, err = store.(state.QueueMessageExpiryStore).ExpirePendingQueueMessages(ctx, time.Now().Add(15*24*time.Hour), 100)
	if err != nil || n != 1 {
		t.Fatalf("expiry 15 days later = %d, %v; want the Scale message", n, err)
	}
	if got := stateOf(scaleWaiting); got.State != state.InvocationDeadLetter {
		t.Fatalf("Scale message waiting 15 days is %s; want dead_letter", got.State)
	}
}

func TestMemQueueDepthReadsStopAtLimit(t *testing.T) {
	testQueueDepthReadsStopAtLimit(t, state.NewMemStore())
}

// The scheduler's depth reads count what QueueState counts, a binding's own
// messages plus legacy work under its name, but stop at the limit; the
// in-flight check sees only unexpired leases (amendment 2026-10-24).
func testQueueDepthReadsStopAtLimit(t *testing.T, store state.Store) {
	ctx := t.Context()
	app := seedQueueBacklogApp(t, store, seedQueueBacklogAccount(t, store, api.PlanPro))
	binding, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: app.AccountID, AppID: app.ID, Name: "jobs",
		QueueName: "jobs", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(bindingID string) state.Invocation {
		t.Helper()
		inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: app.AccountID, Source: state.InvocationQueue,
			QueueName: "jobs", QueueBindingID: bindingID, DeploymentScope: state.DefaultEnvScope, DueAt: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	first := enqueue(binding.ID)
	for range 5 {
		enqueue(binding.ID)
	}
	for range 3 {
		enqueue("")
	}
	if err := store.CancelInvocation(ctx, enqueue(binding.ID).ID); err != nil {
		t.Fatal(err)
	}
	depth := func(scope string, limit int) (int, int) {
		t.Helper()
		inScope, err := store.QueueDepthInScope(ctx, app.ID, state.DefaultEnvScope, limit)
		if err != nil {
			t.Fatal(err)
		}
		forBinding, err := store.QueueDepthForBindingInScope(ctx, app.ID, binding.ID, scope, limit)
		if err != nil {
			t.Fatal(err)
		}
		return inScope, forBinding
	}
	for _, scope := range []string{state.DefaultEnvScope, ""} {
		if inScope, forBinding := depth(scope, 100); inScope != 9 || forBinding != 9 {
			t.Fatalf("scope %q: depth = %d / %d, want 9 / 9 (6 bound + 3 legacy, cancelled excluded)", scope, inScope, forBinding)
		}
		if inScope, forBinding := depth(scope, 4); inScope != 4 || forBinding != 4 {
			t.Fatalf("scope %q: depth limited to 4 = %d / %d", scope, inScope, forBinding)
		}
	}
	if inFlight, err := store.QueueBindingHasInFlight(ctx, app.ID, binding.ID, state.DefaultEnvScope); err != nil || inFlight {
		t.Fatalf("in flight before any lease = %v, %v", inFlight, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, first.ID, "", 600, 10); err != nil {
		t.Fatal(err)
	}
	if inFlight, err := store.QueueBindingHasInFlight(ctx, app.ID, binding.ID, ""); err != nil || !inFlight {
		t.Fatalf("in flight under a lease = %v, %v", inFlight, err)
	}
	if inScope, forBinding := depth(state.DefaultEnvScope, 100); inScope != 9 || forBinding != 9 {
		t.Fatalf("a leased message still counts: depth = %d / %d, want 9 / 9", inScope, forBinding)
	}
	if _, err := store.QueueDepthInScope(ctx, app.ID, state.DefaultEnvScope, 0); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("limit 0 err = %v, want ErrInvalidArgument", err)
	}
}
