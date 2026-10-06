// adr: 590
package state_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentQueueTriggerTestStore interface {
	invocationEnvironmentTestStore
	ListDueInvocationsAfter(context.Context, time.Time, state.InvocationDueCursor, int) ([]state.Invocation, error)
}

func TestMemStageDelayedTasksIgnoreProductionQueueTrigger(t *testing.T) {
	testStageDelayedTasksIgnoreProductionQueueTrigger(t, state.NewMemStore())
}

func testStageDelayedTasksIgnoreProductionQueueTrigger(t *testing.T, store environmentQueueTriggerTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedInvocationEnvironment(t, store)
	due, deadline := time.Now().Add(-time.Second), time.Now().Add(time.Hour)
	prepared, _, err := state.ResolveInvocationVersionForEnvironment(ctx, store, state.Invocation{
		AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationDelayedTask, Method: "POST", Path: "/delayed",
		DueAt: due, DeadlineAt: &deadline, RetryPolicyJSON: []byte(`{"max_attempts":3}`), Payload: []byte(`{"stage":true}`),
	}, "staging")
	if err != nil {
		t.Fatal(err)
	}
	stage, err := store.EnqueueInvocation(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	production, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationDelayedTask, DueAt: due})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateTriggerIfUnderQuota(ctx, f.app.ID, "queue", "timers", true, []byte(`{"mode":"delayed_task"}`),
		"delayed_task", 1, 20, 3, 8192, "commit", api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	for _, paged := range []bool{false, true} {
		var rows []state.Invocation
		if paged {
			rows, err = store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 10)
		} else {
			rows, err = store.ListDueInvocations(ctx, time.Now(), 10)
		}
		if err != nil || len(rows) != 1 || rows[0].ID != stage.ID || !reflect.DeepEqual(rows[0], stage) {
			t.Fatalf("stage delayed work hidden by production trigger (paged=%v): %+v, %v", paged, rows, err)
		}
	}
	claimed, err := store.ClaimInvocationWithCap(ctx, stage.ID, "", 30, 2)
	if err != nil || !claimed.QuotaReserved || claimed.EnvironmentID != stage.EnvironmentID {
		t.Fatalf("owned delayed claim bypassed quota/ownership: %+v, %v", claimed, err)
	}
	if _, current, err := store.GetAccountAsyncQuota(ctx, f.account.ID); err != nil || current != 1 {
		t.Fatalf("stage delayed claim did not reserve account capacity: %d, %v", current, err)
	}
	if got, err := store.InvocationByID(ctx, production.ID); err != nil || got.State != state.InvocationPending || got.Attempts != 0 {
		t.Fatalf("generic drain changed production trigger work: %+v, %v", got, err)
	}
}
