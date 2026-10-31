package sched_test

// adr: 20261031100000

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
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
// is retried with the event backoff; any other 4xx is final and goes to the
// dead letters at once. A plain async invocation keeps the gateway's verdict.
func TestEventDeliveryHandlerFailuresRetryOrDeadLetter(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		status int
		event  bool
		want   state.InvocationState
	}{
		{"thrown handler error retries", http.StatusInternalServerError, true, state.InvocationPending},
		{"request timeout retries", http.StatusRequestTimeout, true, state.InvocationPending},
		{"too many requests retries", http.StatusTooManyRequests, true, state.InvocationPending},
		{"refused input dead-letters", http.StatusBadRequest, true, state.InvocationDeadLetter},
		{"not found dead-letters", http.StatusNotFound, true, state.InvocationDeadLetter},
		{"plain async invocation fails", http.StatusInternalServerError, false, state.InvocationFailed},
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
				inv = sched.SeedDrainInvocation(t, store, state.InvocationAsyncInvoke)
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
	budgets, ok := store.(state.FinancialBudgetStore)
	if !ok {
		t.Fatal("harness store has no financial budgets")
	}
	if _, err := budgets.CreateFinancialBudget(ctx, inv.AccountID, uuid.NewString(), "test", financial.BudgetSpec{
		Name: "compute cap", Scope: financial.BudgetScope{Kind: "account"}, Currency: "USD", Meters: []string{"compute"},
		Basis: "gross_usage", LimitMillicents: 0, Mode: "strict", Action: "suspend_workloads", ResumeRule: "manual", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

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
