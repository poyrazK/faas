// adr: 20261018210500
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type concurrencyTestStore interface {
	scheduleTestStore
	state.OperationConcurrencyStore
	AdmitOperation(ctx context.Context, admission state.OperationAdmission) (state.Operation, bool, error)
	CancelOperation(ctx context.Context, accountID, tenantID, operationID string, generation int) (state.Operation, error)
	InvocationByID(ctx context.Context, id string) (state.Invocation, error)
}

type concurrencyFixture struct {
	scheduleFixture
	store concurrencyTestStore
	app   string
}

func newConcurrencyFixture(t *testing.T, store concurrencyTestStore) concurrencyFixture {
	t.Helper()
	f := concurrencyFixture{scheduleFixture: newScheduleFixture(t, store, api.PlanPro), store: store}
	def, err := store.(interface {
		OperationDefinitionByID(context.Context, string, string) (state.OperationDefinition, error)
	}).OperationDefinitionByID(t.Context(), f.account, f.customer)
	if err != nil {
		t.Fatal(err)
	}
	f.app = def.AppID
	t.Setenv("FAAS_OPERATION_CONCURRENCY_ACCOUNTS", f.account)
	return f
}

func (f concurrencyFixture) admit(t *testing.T, tenant, key string) state.Operation {
	t.Helper()
	op, _, err := f.store.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: f.account, DefinitionID: f.customer, PlatformTenantID: tenant, IdempotencyKey: key, Input: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func (f concurrencyFixture) due(t *testing.T, op state.Operation) bool {
	t.Helper()
	inv, err := f.store.InvocationByID(t.Context(), op.CurrentInvocationID)
	if err != nil {
		t.Fatal(err)
	}
	return !inv.DueAt.After(time.Now().Add(time.Hour))
}

func (f concurrencyFixture) put(t *testing.T, tenant string, max int) api.OperationConcurrencyPolicy {
	t.Helper()
	policy, _, err := f.store.PutOperationConcurrencyPolicy(t.Context(), state.OperationConcurrencyPolicyInput{AccountID: f.account, AppID: f.app,
		Request: api.OperationConcurrencyPolicyRequest{Operation: "export", PlatformTenantID: tenant, MaxRunning: max}, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func runConcurrencyStores(t *testing.T, test func(t *testing.T, f concurrencyFixture)) {
	t.Run("mem", func(t *testing.T) { test(t, newConcurrencyFixture(t, state.NewMemStore())) })
	t.Run("pg", func(t *testing.T) {
		s, _ := pgStore(t)
		test(t, newConcurrencyFixture(t, s))
	})
}

func TestOperationConcurrencyPolicies(t *testing.T) {
	runConcurrencyStores(t, func(t *testing.T, f concurrencyFixture) {
		ctx := t.Context()
		for _, bad := range []api.OperationConcurrencyPolicyRequest{
			{Operation: "export", MaxRunning: 0},
			{Operation: "export", MaxRunning: api.OperationConcurrencyMaxRunning + 1},
			{Operation: "Export!", MaxRunning: 1},
			{Operation: "export", Scope: "not a scope", MaxRunning: 1},
		} {
			if _, _, err := f.store.PutOperationConcurrencyPolicy(ctx, state.OperationConcurrencyPolicyInput{AccountID: f.account, AppID: f.app, Request: bad, Now: time.Now()}); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("policy %+v: %v", bad, err)
			}
		}
		if _, _, err := f.store.PutOperationConcurrencyPolicy(ctx, state.OperationConcurrencyPolicyInput{AccountID: f.account, AppID: f.app,
			Request: api.OperationConcurrencyPolicyRequest{Operation: "export", PlatformTenantID: uuid.NewString(), MaxRunning: 1}, Now: time.Now()}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("policy for an unknown customer: %v", err)
		}
		first, created, err := f.store.PutOperationConcurrencyPolicy(ctx, state.OperationConcurrencyPolicyInput{AccountID: f.account, AppID: f.app,
			Request: api.OperationConcurrencyPolicyRequest{Operation: "export", MaxRunning: 2}, Now: time.Now()})
		if err != nil || !created || first.Scope != api.DefaultEnvScope || first.PlatformTenantID != "" {
			t.Fatalf("default policy = %+v created=%v %v", first, created, err)
		}
		again, created, err := f.store.PutOperationConcurrencyPolicy(ctx, state.OperationConcurrencyPolicyInput{AccountID: f.account, AppID: f.app,
			Request: api.OperationConcurrencyPolicyRequest{Operation: "export", MaxRunning: 3}, Now: time.Now()})
		if err != nil || created || again.ID != first.ID || again.MaxRunning != 3 {
			t.Fatalf("replaced policy = %+v created=%v %v", again, created, err)
		}
		override := f.put(t, f.tenant, 5)
		view, err := f.store.OperationConcurrency(ctx, f.account, f.app)
		if err != nil || len(view.Policies) != 2 || view.Policies[0].ID != first.ID || view.Policies[1].ID != override.ID {
			t.Fatalf("policies = %+v %v", view.Policies, err)
		}
		if err := f.store.DeleteOperationConcurrencyPolicy(ctx, f.account, f.app, override.ID); err != nil {
			t.Fatal(err)
		}
		if err := f.store.DeleteOperationConcurrencyPolicy(ctx, f.account, f.app, override.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("second delete: %v", err)
		}
	})
}

func TestOperationConcurrencyQueuesAndReleasesInOrder(t *testing.T) {
	runConcurrencyStores(t, func(t *testing.T, f concurrencyFixture) {
		ctx := t.Context()
		f.put(t, "", 2)
		a1, a2, a3, a4 := f.admit(t, f.tenant, "a1"), f.admit(t, f.tenant, "a2"), f.admit(t, f.tenant, "a3"), f.admit(t, f.tenant, "a4")
		b1 := f.admit(t, f.other, "b1")
		for _, c := range []struct {
			op  state.Operation
			due bool
		}{{a1, true}, {a2, true}, {a3, false}, {a4, false}, {b1, true}} {
			if got := f.due(t, c.op); got != c.due || c.op.State != api.OperationAccepted {
				t.Fatalf("operation %s due=%v state=%s, want due=%v", c.op.ID, got, c.op.State, c.due)
			}
		}
		queue, err := f.store.CustomerOperationQueue(ctx, f.account, f.tenant)
		if err != nil || len(queue) != 2 || queue[0].OperationID != a3.ID || queue[0].Position != 1 || queue[1].OperationID != a4.ID || queue[1].Position != 2 {
			t.Fatalf("queue = %+v %v", queue, err)
		}
		if other, err := f.store.CustomerOperationQueue(ctx, f.account, f.other); err != nil || len(other) != 0 {
			t.Fatalf("other customer's queue = %+v %v", other, err)
		}
		view, err := f.store.OperationConcurrency(ctx, f.account, f.app)
		if err != nil || len(view.Queues) != 1 || view.Queues[0].PlatformTenantID != f.tenant || view.Queues[0].Limit != 2 || view.Queues[0].InFlight != 2 || view.Queues[0].Queued != 2 {
			t.Fatalf("usage = %+v %v", view.Queues, err)
		}
		if n, err := f.store.ReleaseQueuedOperations(ctx, time.Now()); err != nil || n != 0 {
			t.Fatalf("release with no free slot = %d %v", n, err)
		}

		// A finished Operation frees a slot: the oldest queued one starts.
		if _, err := f.store.CancelOperation(ctx, f.account, "", a1.ID, a1.Generation); err != nil {
			t.Fatal(err)
		}
		if n, err := f.store.ReleaseQueuedOperations(ctx, time.Now()); err != nil || n != 1 || !f.due(t, a3) || f.due(t, a4) {
			t.Fatalf("release after a slot freed = %d %v (a3 due %v, a4 due %v)", n, err, f.due(t, a3), f.due(t, a4))
		}
		// A new submission waits behind the queue even with a slot free.
		if _, err := f.store.CancelOperation(ctx, f.account, "", a2.ID, a2.Generation); err != nil {
			t.Fatal(err)
		}
		a5 := f.admit(t, f.tenant, "a5")
		if f.due(t, a5) {
			t.Fatal("a new Operation jumped the queue")
		}
		// A per-customer override raises this customer's limit.
		f.put(t, f.tenant, 3)
		if n, err := f.store.ReleaseQueuedOperations(ctx, time.Now()); err != nil || n != 2 || !f.due(t, a4) || !f.due(t, a5) {
			t.Fatalf("release after the override = %d %v", n, err)
		}
		if queue, _ := f.store.CustomerOperationQueue(ctx, f.account, f.tenant); len(queue) != 0 {
			t.Fatalf("queue after release = %+v", queue)
		}
	})
}

func TestOperationConcurrencyCleansCancelledAndClosedQueues(t *testing.T) {
	runConcurrencyStores(t, func(t *testing.T, f concurrencyFixture) {
		ctx := t.Context()
		f.put(t, "", 1)
		f.admit(t, f.tenant, "c1")
		queued, closed := f.admit(t, f.tenant, "c2"), f.admit(t, f.tenant, "c3")
		if _, err := f.store.CancelOperation(ctx, f.account, "", queued.ID, queued.Generation); err != nil {
			t.Fatal(err)
		}
		if n, err := f.store.ReleaseQueuedOperations(ctx, time.Now()); err != nil || n != 0 {
			t.Fatalf("release = %d %v", n, err)
		}
		if queue, _ := f.store.CustomerOperationQueue(ctx, f.account, f.tenant); len(queue) != 1 || queue[0].OperationID != closed.ID || queue[0].Position != 1 {
			t.Fatalf("queue after a queued cancel = %+v", queue)
		}
		// Closing the rollout stops queueing and releases what is held.
		t.Setenv("FAAS_OPERATION_CONCURRENCY_ACCOUNTS", "")
		if open := f.admit(t, f.tenant, "c4"); !f.due(t, open) {
			t.Fatal("closed rollout still queued new work")
		}
		if n, err := f.store.ReleaseQueuedOperations(ctx, time.Now()); err != nil || n != 1 || !f.due(t, closed) {
			t.Fatalf("closed rollout release = %d %v", n, err)
		}
	})
}
