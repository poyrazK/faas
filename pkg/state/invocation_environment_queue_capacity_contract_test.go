// adr: 531
package state_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemEnvironmentQueueProducerDepthIsAtomicAndIsolated(t *testing.T) {
	testEnvironmentQueueProducerDepth(t, state.NewMemStore())
}

func testEnvironmentQueueProducerDepth(t *testing.T, store environmentQueueInvocationTestStore) {
	t.Helper()
	ctx := t.Context()
	f := seedQueueConsumers(t, store)
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	bindings := append([]state.ProjectEnvironmentQueueDefinition(nil), f.spec.Settings.QueueBindings.Bindings...)
	for i := range bindings {
		if bindings[i].Name == "retry" {
			bindings[i].Enabled = true
		}
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", f.spec.Revision, bindings); err != nil {
		t.Fatal(err)
	}
	newer, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, newer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "other", 0, bindings); err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	// The authoritative account plan, rather than the old caller-side account,
	// must control admission. Existing live deployments remain pinned.
	if err := store.UpdateAccountPlan(ctx, f.account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	limit := api.MustLimitsFor(api.PlanHobby).MaxQueueDepth
	for range limit + 2 {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: f.app.ID, AccountID: f.account.ID, Source: state.InvocationQueue, DueAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	var rows []state.Invocation
	for i := range limit - 1 {
		dep, name := f.dep.ID, "orders"
		if i%2 == 1 {
			dep, name = newer.ID, "retry"
		}
		inv, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, dep, name, state.Invocation{})
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, inv)
	}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	var rejected sync.Map
	for i := range 16 {
		wg.Go(func() {
			id := uuid.NewString()
			dep, name := f.dep.ID, "orders"
			if i%2 == 1 {
				dep, name = newer.ID, "retry"
			}
			_, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, dep, name, state.Invocation{ID: id})
			if err == nil {
				admitted.Add(1)
				return
			}
			if !errors.Is(err, state.ErrQuotaExceeded) {
				t.Errorf("concurrent producer: %v", err)
			}
			rejected.Store(id, true)
		})
	}
	wg.Wait()
	if admitted.Load() != 1 {
		t.Fatalf("depth admission raced across names/generations: %d", admitted.Load())
	}
	rejected.Range(func(key, value any) bool {
		id := key.(string)
		if _, err := store.InvocationByID(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("rejected producer left row: %v", err)
		}
		if _, err := store.InvocationEnvironmentQueueAdmission(ctx, id); !errors.Is(err, state.ErrNotFound) {
			t.Errorf("rejected producer left proof: %v", err)
		}
		return true
	})
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, other.ID, "orders", state.Invocation{}); err != nil {
		t.Fatalf("full stage blocked sibling: %v", err)
	}
	if n, err := store.CountPendingInvocations(ctx, f.app.ID, state.InvocationQueue); err != nil || n != limit+2 {
		t.Fatalf("stage depth changed production: %d %v", n, err)
	}
	for _, inv := range rows[:2] {
		if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", -1, 4); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, newer.ID, "retry", state.Invocation{}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("expired unreaped claims freed depth: %v", err)
	}
	if err := store.CancelInvocation(ctx, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, newer.ID, "retry", state.Invocation{}); err != nil {
		t.Fatalf("cancel failed to free depth: %v", err)
	}
	if err := store.CompleteInvocation(ctx, rows[1].ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{}); err != nil {
		t.Fatalf("completion failed to free depth: %v", err)
	}
	if err := store.UpdateAccountPlan(ctx, f.account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, other.ID, "orders", state.Invocation{}); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("free plan admitted stage work: %v", err)
	}
	if err := store.UpdateAccountPlan(ctx, f.account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{}); err != nil {
		t.Fatalf("plan upgrade did not admit stage work: %v", err)
	}
	if _, cur, err := store.GetAccountAsyncQuota(ctx, f.account.ID); err != nil || cur != 0 {
		t.Fatalf("producer failures changed account claim quota: %d %v", cur, err)
	}
}
