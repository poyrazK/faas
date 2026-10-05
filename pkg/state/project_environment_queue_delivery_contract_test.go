// adr: 585
package state_test

import "context"

import (
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type environmentQueueDeliveryTestStore interface {
	environmentQueueInvocationTestStore
	state.ProjectEnvironmentQueueDeliveryStore
	state.AccountAbuseHoldStore
}

func seedQueueDelivery(ctx context.Context, t *testing.T, store environmentQueueDeliveryTestStore, class state.WorkloadClass, mode string) (queueConsumerFixture, state.ProjectEnvironmentQueueDeliveryRequest) {
	t.Helper()
	var f queueConsumerFixture
	var err error
	f.account, err = store.CreateAccount(ctx, "delivery-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	f.project, err = store.CreateProject(ctx, state.Project{AccountID: f.account.ID, Slug: "delivery"})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"stage", "other"} {
		if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: slug}); err != nil {
			t.Fatal(err)
		}
	}
	appType := state.AppTypeApp
	if class == state.WorkloadClassHTTP {
		appType = state.AppTypeFunction
	}
	f.app, err = store.CreateApp(ctx, state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: "delivery-" + uuid.NewString(), WorkloadName: "delivery",
		Type: appType, WorkloadClass: class, RAMMB: 256, MaxConcurrency: 4, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	f.spec, err = state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", 0, []state.ProjectEnvironmentQueueDefinition{
		{Name: "orders", QueueName: "orders", Mode: mode, WorkloadClass: class, Enabled: true, MaxConcurrency: 2, RetryPolicyJSON: []byte(`{"max_attempts":2,"base_seconds":0.000001}`)},
		{Name: "disabled", QueueName: "disabled", Mode: mode, WorkloadClass: class, Enabled: false, MaxConcurrency: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.dep, err = store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "stage", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	req := state.ProjectEnvironmentQueueDeliveryRequest{ProjectEnvironmentQueueDeliveryScope: state.ProjectEnvironmentQueueDeliveryScope{
		AccountID: f.account.ID, ProjectID: f.project.ID, DeploymentID: f.dep.ID, BindingName: "orders"}, Mode: mode, LeaseSeconds: 30}
	return f, req
}

func TestMemEnvironmentQueueDeliveryClassesAndReceipts(t *testing.T) {
	testEnvironmentQueueDeliveryClassesAndReceipts(t, state.NewMemStore())
}

func testEnvironmentQueueDeliveryClassesAndReceipts(t *testing.T, store environmentQueueDeliveryTestStore) {
	ctx := t.Context()
	for _, transport := range []struct {
		class state.WorkloadClass
		mode  string
	}{{state.WorkloadClassWorker, "pull"}, {state.WorkloadClassJob, "pull"}, {state.WorkloadClassWorker, "push"}, {state.WorkloadClassJob, "push"}, {state.WorkloadClassHTTP, "push"}} {
		t.Run(string(transport.class)+"_"+transport.mode, func(t *testing.T) {
			f, req := seedQueueDelivery(ctx, t, store, transport.class, transport.mode)
			inv := enqueueStageQueue(ctx, t, store, f)
			first, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
			if err != nil || first.Invocation.ID != inv.ID || first.Invocation.State != state.InvocationDispatching || first.Invocation.Attempts != 1 ||
				!first.Invocation.QuotaReserved || first.Invocation.EnvironmentID != f.spec.EnvironmentID || first.Receipt == "" ||
				first.Consumer.Mode != transport.mode || first.Consumer.WorkloadClass != transport.class || !reflect.DeepEqual(first.Invocation.Payload, inv.Payload) {
				t.Fatalf("delivery: %+v %v", first, err)
			}
			if err := store.CompleteInvocation(ctx, inv.ID, nil); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("completion bypassed receipt: %v", err)
			}
			if err := store.FailInvocation(ctx, inv.ID, "bypass", time.Second, 99); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("failure bypassed receipt: %v", err)
			}
			wrongScope := req.ProjectEnvironmentQueueDeliveryScope
			wrongScope.DeploymentID = uuid.NewString()
			if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, wrongScope, inv.ID, first.Receipt, nil); err == nil {
				t.Fatal("foreign deployment completed delivery")
			}
			if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, "invalid", nil); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatalf("invalid receipt: %v", err)
			}
			// A desired edit cannot change retry behavior of this pinned delivery.
			bindings := append([]state.ProjectEnvironmentQueueDefinition{}, f.spec.Settings.QueueBindings.Bindings...)
			for i := range bindings {
				bindings[i].RetryPolicyJSON = []byte(`{"max_attempts":9}`)
			}
			if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "stage", f.spec.Revision, bindings); err != nil {
				t.Fatal(err)
			}
			if err := store.RetryProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, first.Receipt, "transient"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 30, 10000); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("generic reclaim bypassed receipt: %v", err)
			}
			second, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
			if err != nil || second.Invocation.Attempts != 2 || second.Receipt == first.Receipt || second.Consumer.ID != first.Consumer.ID {
				t.Fatalf("redelivery did not rotate receipt: %+v %v", second, err)
			}
			if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, first.Receipt, nil); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale completion: %v", err)
			}
			if err := store.RetryProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, first.Receipt, "stale"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("stale retry: %v", err)
			}
			if err := store.RetryProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, second.Receipt, "poison"); err != nil {
				t.Fatal(err)
			}
			terminal, err := store.InvocationByID(ctx, inv.ID)
			if err != nil || terminal.State != state.InvocationDeadLetter || terminal.Attempts != 2 || terminal.QuotaReserved || terminal.LastError != "poison" || terminal.LeaseExpiresAt != nil {
				t.Fatalf("pinned retry budget not honored: %+v %v", terminal, err)
			}
			if err := store.RetryProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, second.Receipt, "duplicate"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("duplicate retry: %v", err)
			}
			_, inflight, err := store.GetAccountAsyncQuota(ctx, f.account.ID)
			if err != nil || inflight != 0 {
				t.Fatalf("receipt leaked/double released quota: %d %v", inflight, err)
			}
		})
	}
}

func TestMemEnvironmentQueueDeliveryScopeAndCapacity(t *testing.T) {
	testEnvironmentQueueDeliveryScopeAndCapacity(t, state.NewMemStore())
}

func testEnvironmentQueueDeliveryScopeAndCapacity(t *testing.T, store environmentQueueDeliveryTestStore) {
	ctx := t.Context()
	f, req := seedQueueDelivery(t.Context(), t, store, state.WorkloadClassWorker, "pull")
	for _, mutate := range []func(*state.ProjectEnvironmentQueueDeliveryRequest){
		func(r *state.ProjectEnvironmentQueueDeliveryRequest) { r.Mode = "push" },
		func(r *state.ProjectEnvironmentQueueDeliveryRequest) { r.BindingName = "disabled" },
		func(r *state.ProjectEnvironmentQueueDeliveryRequest) { r.LeaseSeconds = 0 },
		func(r *state.ProjectEnvironmentQueueDeliveryRequest) { r.LeaseSeconds = 1 << 40 },
		func(r *state.ProjectEnvironmentQueueDeliveryRequest) { r.AccountID = uuid.NewString() },
		func(r *state.ProjectEnvironmentQueueDeliveryRequest) { r.ProjectID = uuid.NewString() },
	} {
		bad := req
		mutate(&bad)
		if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, bad); err == nil {
			t.Fatalf("invalid delivery scope accepted: %+v", bad)
		}
	}
	// Empty reads and rejected requests do not even create a quota row.
	if _, _, err := store.GetAccountAsyncQuota(ctx, f.account.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty/rejected read changed quota: %v", err)
	}
	if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("empty stage: %v", err)
	}
	production, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: f.account.ID, AppID: f.app.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ReplaceEnvironmentQueueBindings(ctx, store, f.app, "other", 0, f.spec.Settings.QueueBindings.Bindings); err != nil {
		t.Fatal(err)
	}
	siblingDep, err := store.CreateDeployment(ctx, state.Deployment{AppID: f.app.ID, Scope: "other", Kind: state.DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, siblingDep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PrepareProjectEnvironmentQueueConsumers(ctx, f.account.ID, f.project.ID, siblingDep.ID); err != nil {
		t.Fatal(err)
	}
	sibling, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, siblingDep.ID, "orders", state.Invocation{DueAt: time.Now().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	future, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{DueAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(-time.Minute)
	expired, err := store.EnqueueProjectEnvironmentQueueInvocation(ctx, f.account.ID, f.project.ID, f.dep.ID, "orders", state.Invocation{DueAt: time.Now().Add(-time.Hour), DeadlineAt: &deadline})
	if err != nil {
		t.Fatal(err)
	}
	var oldest state.Invocation
	for i := range 8 {
		inv := enqueueStageQueue(t.Context(), t, store, f)
		if i == 0 {
			oldest = inv
		}
	}
	if _, _, err := store.EnsureAccountAsyncQuota(ctx, f.account.ID, 1); err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Invocation.ID != oldest.ID {
		t.Fatalf("receive did not select oldest due message: got %s want %s", first.Invocation.ID, oldest.ID)
	}
	if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("existing account cap overwritten: %v", err)
	}
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, first.Invocation.ID, first.Receipt, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.EnsureAccountAsyncQuota(ctx, f.account.ID, 100); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	var mu sync.Mutex
	deliveries := []state.ProjectEnvironmentQueueDelivery{}
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			delivery, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
			if errors.Is(err, state.ErrQuotaExceeded) {
				return
			}
			if err != nil {
				t.Errorf("concurrent receive: %v", err)
				return
			}
			count.Add(1)
			mu.Lock()
			deliveries = append(deliveries, delivery)
			mu.Unlock()
		})
	}
	wg.Wait()
	if count.Load() != 2 || len(deliveries) != 2 || deliveries[0].Invocation.ID == deliveries[1].Invocation.ID {
		t.Fatalf("consumer cap or unique claim lost: %d %+v", count.Load(), deliveries)
	}
	for _, inv := range []state.Invocation{production, sibling, future, expired} {
		unchanged, err := store.InvocationByID(ctx, inv.ID)
		if err != nil || unchanged.State != state.InvocationPending || unchanged.Attempts != 0 || unchanged.QuotaReserved {
			t.Fatalf("scoped receive adopted excluded message: %+v %v", unchanged, err)
		}
	}
	var completed atomic.Int32
	for range 12 {
		wg.Go(func() {
			d := deliveries[0]
			err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, d.Invocation.ID, d.Receipt, nil)
			if err == nil {
				completed.Add(1)
			} else if !errors.Is(err, state.ErrNotFound) {
				t.Errorf("concurrent completion: %v", err)
			}
		})
	}
	wg.Wait()
	if completed.Load() != 1 {
		t.Fatalf("completion accepted %d times", completed.Load())
	}
	d := deliveries[1]
	if err := store.CancelInvocation(ctx, d.Invocation.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, d.Invocation.ID, d.Receipt, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("cancelled receipt acknowledged: %v", err)
	}
	_, inflight, err := store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || inflight != 0 {
		t.Fatalf("concurrent completion/cancel quota: %d %v", inflight, err)
	}
	if changed, err := store.SetAccountAbuseHold(ctx, f.account.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil || !changed {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("abuse hold bypassed: %v", err)
	}
	if _, err := store.ReleaseAccountAbuseHold(ctx, f.account.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, f.account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); !errors.Is(err, state.ErrQuotaExceeded) {
		t.Fatalf("Free plan bypassed: %v", err)
	}
}

func TestMemEnvironmentQueueDeliveryLeaseRecoveryAndRetirement(t *testing.T) {
	testEnvironmentQueueDeliveryLeaseRecoveryAndRetirement(t, state.NewMemStore())
}

func testEnvironmentQueueDeliveryLeaseRecoveryAndRetirement(t *testing.T, store environmentQueueDeliveryTestStore) {
	ctx := t.Context()
	f, req := seedQueueDelivery(t.Context(), t, store, state.WorkloadClassJob, "pull")
	inv := enqueueStageQueue(t.Context(), t, store, f)
	req.LeaseSeconds = 1
	first, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Until(*first.Invocation.LeaseExpiresAt) + 10*time.Millisecond)
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, first.Receipt, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("expired lease completed before reaping: %v", err)
	}
	if requeued, err := store.RequeueExpiredInvocations(ctx, time.Now(), 100); err != nil || requeued != 1 {
		t.Fatalf("recover: %d %v", requeued, err)
	}
	req.LeaseSeconds = 30
	second, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
	if err != nil || second.Invocation.ID != inv.ID || second.Invocation.Attempts != 2 || second.Receipt == first.Receipt {
		t.Fatalf("recovered delivery: %+v %v", second, err)
	}
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, first.Receipt, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("recovered lease accepted old receipt: %v", err)
	}
	if err := store.MarkDeploymentSuperseded(ctx, f.dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); err == nil {
		t.Fatal("retired deployment claimed")
	}
	result := json.RawMessage(`{"job":"finished"}`)
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, second.Receipt, result); err != nil {
		t.Fatalf("retirement stranded current lease: %v", err)
	}
	result[0] = '!'
	finished, err := store.InvocationByID(ctx, inv.ID)
	var jobResult struct {
		Job string `json:"job"`
	}
	decodeErr := json.Unmarshal(finished.Result, &jobResult)
	if err != nil || decodeErr != nil || finished.State != state.InvocationCompleted || jobResult.Job != "finished" || finished.QuotaReserved {
		t.Fatalf("completion result aliases caller or quota leaked: %+v %v", finished, err)
	}
	if n, err := store.DeleteInvocationsByIDs(ctx, []string{inv.ID}); err != nil || n != 1 {
		t.Fatalf("receipt cascaded retention: %d %v", n, err)
	}
}

func TestMemEnvironmentQueueDeliveryLeaseTimeoutsHonorAttemptBudget(t *testing.T) {
	testEnvironmentQueueDeliveryLeaseTimeoutsHonorAttemptBudget(t, state.NewMemStore())
}

func testEnvironmentQueueDeliveryLeaseTimeoutsHonorAttemptBudget(t *testing.T, store environmentQueueDeliveryTestStore) {
	ctx := t.Context()
	f, req := seedQueueDelivery(t.Context(), t, store, state.WorkloadClassWorker, "pull")
	inv := enqueueStageQueue(t.Context(), t, store, f)
	req.LeaseSeconds = 1
	var last state.ProjectEnvironmentQueueDelivery
	for attempt := 1; attempt <= 2; attempt++ {
		delivery, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
		if err != nil || delivery.Invocation.ID != inv.ID || delivery.Invocation.Attempts != attempt {
			t.Fatalf("attempt %d: %+v %v", attempt, delivery, err)
		}
		last = delivery
		time.Sleep(time.Until(*delivery.Invocation.LeaseExpiresAt) + 10*time.Millisecond)
		if n, err := store.RequeueExpiredInvocations(ctx, time.Now(), 10); err != nil || n != 1 {
			t.Fatalf("timeout recovery: %d %v", n, err)
		}
	}
	next := enqueueStageQueue(t.Context(), t, store, f)
	if _, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("exhausted message delivered a third time: %v", err)
	}
	terminal, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || terminal.State != state.InvocationDeadLetter || terminal.Attempts != 2 || terminal.QuotaReserved || terminal.CompletedAt == nil {
		t.Fatalf("timeout budget not terminal: %+v %v", terminal, err)
	}
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, inv.ID, last.Receipt, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("exhausted receipt completed: %v", err)
	}
	_, inflight, err := store.GetAccountAsyncQuota(ctx, f.account.ID)
	if err != nil || inflight != 0 {
		t.Fatalf("exhausted message reserved quota: %d %v", inflight, err)
	}
	delivery, err := store.ClaimNextProjectEnvironmentQueueDelivery(ctx, req)
	if err != nil || delivery.Invocation.ID != next.ID || delivery.Invocation.Attempts != 1 {
		t.Fatalf("exhausted message starved next delivery: %+v %v", delivery, err)
	}
	if err := store.CompleteProjectEnvironmentQueueDelivery(ctx, req.ProjectEnvironmentQueueDeliveryScope, next.ID, delivery.Receipt, nil); err != nil {
		t.Fatal(err)
	}
}
