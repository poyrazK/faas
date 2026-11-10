package sched_test

// adr: 20261031100001

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func seedEventDelivery(t *testing.T, store state.Store) state.Invocation {
	t.Helper()
	apps, err := store.ListAllApps(context.Background())
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	inv, err := store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: apps[0].ID, AccountID: apps[0].AccountID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/events", DueAt: time.Now().Add(-time.Second),
		Headers: json.RawMessage(`{"x-gregale-event-subscription-id":"` + uuid.NewString() + `"}`),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	return inv
}

// A handler that throws (the runner's 500) or asks to be retried (408, 429)
// is retried with the invocation's backoff; any other 4xx is final and goes
// to the dead letters at once. Since the 2026-11-07 amendment this holds for
// every durable async source, not only event deliveries: a thrown handler on
// a plain async invocation, an inbound webhook, a queue message or a delayed
// task used to fail after one attempt with no dead letter. A cron run keeps
// the gateway's verdict.
func TestEventDeliveryHandlerFailuresRetryOrDeadLetter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		event  bool
		source state.InvocationSource
		want   state.InvocationState
	}{
		{"thrown handler error retries", http.StatusInternalServerError, true, "", state.InvocationPending},
		{"request timeout retries", http.StatusRequestTimeout, true, "", state.InvocationPending},
		{"too many requests retries", http.StatusTooManyRequests, true, "", state.InvocationPending},
		{"refused input dead-letters", http.StatusBadRequest, true, "", state.InvocationDeadLetter},
		{"not found dead-letters", http.StatusNotFound, true, "", state.InvocationDeadLetter},
		{"thrown plain async invocation retries", http.StatusInternalServerError, false, state.InvocationAsyncInvoke, state.InvocationPending},
		{"thrown inbound webhook retries", http.StatusInternalServerError, false, state.InvocationInboundWebhook, state.InvocationPending},
		{"thrown queue message retries", http.StatusInternalServerError, false, state.InvocationQueue, state.InvocationPending},
		{"thrown delayed task retries", http.StatusInternalServerError, false, state.InvocationDelayedTask, state.InvocationPending},
		{"refused plain async invocation dead-letters", http.StatusUnprocessableEntity, false, state.InvocationAsyncInvoke, state.InvocationDeadLetter},
		{"thrown cron run fails", http.StatusInternalServerError, false, state.InvocationCron, state.InvocationFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanPro, true)
			ds.AppStatus().Store(int64(tc.status))
			var inv state.Invocation
			if tc.event {
				inv = seedEventDelivery(t, store)
			} else {
				inv = sched.SeedDrainInvocation(t, store, tc.source)
			}

			d.Tick(ctx)

			got, err := store.InvocationByID(ctx, inv.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != tc.want {
				t.Fatalf("HTTP %d leaves the row %s, want %s", tc.status, got.State, tc.want)
			}
			if got.Attempts != 1 {
				t.Fatalf("attempts = %d, want 1", got.Attempts)
			}
			if tc.want == state.InvocationPending && !got.DueAt.After(time.Now()) {
				t.Fatalf("a retried delivery is due %s, want later than now", got.DueAt)
			}
		})
	}
}

// A thrown handler error dead-letters once the delivery's attempts run out.
func TestEventDeliveryHandlerErrorDeadLettersWhenAttemptsRunOut(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanPro, true)
	ds.AppStatus().Store(http.StatusInternalServerError)
	inv := seedEventDelivery(t, store)
	policy := []byte(`{"max_attempts":1,"base_seconds":1}`)
	if _, err := store.UpdateApp(ctx, inv.AppID, state.UpdateAppParams{RetryPolicyJSON: &policy, SetRetryPolicy: true}); err != nil {
		t.Fatal(err)
	}

	d.Tick(ctx)

	got, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.InvocationDeadLetter {
		t.Fatalf("an exhausted thrown delivery is %s, want dead_letter", got.State)
	}
}

// A thrown inbound webhook (a Stripe event the provider already saw
// accepted) or plain async invocation dead-letters once its attempts run
// out, so it can be replayed instead of being lost (production-us
// 2026-11-07: evt_lost_154634 ran once and left nothing to replay).
func TestThrownAsyncWorkDeadLettersWhenAttemptsRunOut(t *testing.T) {
	t.Parallel()
	for _, source := range []state.InvocationSource{state.InvocationInboundWebhook, state.InvocationAsyncInvoke} {
		t.Run(string(source), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanPro, true)
			ds.AppStatus().Store(http.StatusInternalServerError)
			apps, err := store.ListAllApps(ctx)
			if err != nil || len(apps) == 0 {
				t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
			}
			// Async rows carry the app's retry policy stamped at enqueue.
			inv, err := store.EnqueueInvocation(ctx, state.Invocation{
				AppID: apps[0].ID, AccountID: apps[0].AccountID, Source: source,
				Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
				RetryPolicyJSON: json.RawMessage(`{"max_attempts":1,"base_seconds":1}`),
			})
			if err != nil {
				t.Fatalf("EnqueueInvocation: %v", err)
			}

			d.Tick(ctx)

			got, err := store.InvocationByID(ctx, inv.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != state.InvocationDeadLetter {
				t.Fatalf("an exhausted thrown %s is %s, want dead_letter", source, got.State)
			}
		})
	}
}

// The app's retry policy shapes an event delivery's backoff but cannot wait
// past the event cap: 5 minutes, or 30 s for a keyed delivery.
func TestEventDeliveryAppRetryPolicyIsBounded(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, _ := sched.NewDrainHarness(t, api.PlanPro, false)
	delivery := seedEventDelivery(t, store)
	delivery.Attempts = 15
	for _, tc := range []struct {
		policy string
		keyed  bool
		want   time.Duration
	}{
		{`{"max_attempts":20,"base_seconds":2}`, false, 5 * time.Minute},
		{`{"max_attempts":20,"base_seconds":2}`, true, 30 * time.Second},
		{`{"max_attempts":20,"base_seconds":2,"max_seconds":86400}`, false, 5 * time.Minute},
		{`{"max_attempts":20,"base_seconds":2,"max_seconds":86400}`, true, 5 * time.Minute},
		{`{"max_attempts":20,"base_seconds":2,"max_seconds":10}`, true, 10 * time.Second},
	} {
		policy := []byte(tc.policy)
		if _, err := store.UpdateApp(ctx, delivery.AppID, state.UpdateAppParams{RetryPolicyJSON: &policy, SetRetryPolicy: true}); err != nil {
			t.Fatal(err)
		}
		inv := delivery
		if tc.keyed {
			inv.WorkPolicyName = "orders"
		}
		if got := d.XInvocationRetryDelay(ctx, inv); got != tc.want {
			t.Errorf("policy %s keyed=%v waits %s, want %s", tc.policy, tc.keyed, got, tc.want)
		}
	}
}

// Work of an app held by a strict compute budget moves back a minute
// without spending an attempt, so it no longer sits at the head of every
// walk in front of other tenants' work.
func TestDrainDefersWorkHeldByAComputeBudget(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanPro, true)
	inv := sched.SeedDrainInvocation(t, store, state.InvocationAsyncInvoke)
	d.XHoldFinancialAdmission(state.ErrFinancialBudgetHeld)

	d.Tick(ctx)

	if got := ds.Calls().Load(); got != 0 {
		t.Fatalf("gateway calls = %d, want 0 while the budget holds", got)
	}
	got, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.InvocationPending || !got.DueAt.After(time.Now().Add(30*time.Second)) {
		t.Fatalf("held invocation = %s due %s, want pending and deferred", got.State, got.DueAt)
	}
	if got.Attempts != inv.Attempts {
		t.Fatalf("attempts = %d, want %d: a hold spends no attempt", got.Attempts, inv.Attempts)
	}
}

// A sync invoke's caller already receives the handler's failure, so the row
// ends failed after one attempt instead of retrying for a day (Hunt 32
// H32-65: a thrown sync invoke on prod ran 11 attempts in 23 min with a
// 25-attempt budget, waking the app each time).
func TestThrownSyncInvokeEndsAfterOneAttempt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d, store, _, _, ds := sched.NewDrainHarness(t, api.PlanPro, true)
	ds.AppStatus().Store(http.StatusInternalServerError)
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) == 0 {
		t.Fatalf("ListAllApps: %v / %d apps", err, len(apps))
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: apps[0].ID, AccountID: apps[0].AccountID, Source: state.InvocationAsyncInvoke,
		Method: "POST", Path: "/x", DueAt: time.Now().Add(-time.Second),
		RetryPolicyJSON: state.MarkSyncInvokeRetryPolicy(json.RawMessage(`{"max_attempts":25,"base_seconds":1}`)),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	d.Tick(ctx)

	got, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != state.InvocationFailed || got.Attempts != 1 {
		t.Fatalf("a thrown sync invoke is %s after %d attempts, want failed after 1", got.State, got.Attempts)
	}
}
