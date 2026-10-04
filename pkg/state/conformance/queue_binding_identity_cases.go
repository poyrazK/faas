package conformance

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func testInvocationQueueBindingIdentity(t *testing.T, fx *Fixture) {
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	store := fx.Store.(state.QueueBindingConsumerStore)
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "queue-identity-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateQueueBindingWithConsumer(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, Name: "original", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func(queue, scope string) state.Invocation {
		t.Helper()
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AppID: app.ID, AccountID: fx.Account.ID, Source: state.InvocationQueue, QueueName: queue, DeploymentScope: scope, DueAt: time.Now().Add(-time.Second), Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	old, unnamed, neighbor := enqueue("orders", "default"), enqueue("", "default"), enqueue("orders", "staging")
	for _, inv := range []state.Invocation{old, unnamed, neighbor} {
		if inv.QueueBindingID != created.Binding.ID {
			t.Fatalf("admission lost binding identity: %+v", inv)
		}
	}
	receipt, err := fx.Store.InsertTriggerRecord(fx.Ctx, created.Changes[0].TriggerID, old.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, old.ID, "", 60, 10); err != nil {
		t.Fatal(err)
	}
	if err := fx.Store.FailInvocation(fx.Ctx, old.ID, "exhausted", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
	name := "payments"
	renamed, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name})
	if err != nil || renamed.Changes[0].TriggerID != created.Changes[0].TriggerID {
		t.Fatalf("rename replaced consumer: %+v %v", renamed, err)
	}
	fresh := enqueue("payments", "default")
	replacement, err := fx.Store.CreateQueueBinding(fx.Ctx, state.QueueBinding{AppID: app.ID, AccountID: fx.Account.ID, Name: "replacement", QueueName: "orders", Mode: "pull", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	replacementWork := enqueue("orders", "default")
	if replacementWork.QueueBindingID != replacement.ID {
		t.Fatal("replacement admission lost new owner")
	}
	stats, err := fx.Store.QueueStateForBinding(fx.Ctx, app.ID, created.Binding.ID)
	if err != nil || stats.Depth != 3 || stats.DeadLetter != 1 {
		t.Fatalf("renamed aggregate=%+v %v", stats, err)
	}
	scoped, err := fx.Store.QueueStateForBindingInScope(fx.Ctx, app.ID, created.Binding.ID, "default")
	if err != nil || scoped.Depth != 2 || scoped.DeadLetter != 1 {
		t.Fatalf("renamed scope=%+v %v", scoped, err)
	}
	other, err := fx.Store.QueueStateForBinding(fx.Ctx, app.ID, replacement.ID)
	if err != nil || other.Depth != 1 || other.DeadLetter != 0 {
		t.Fatalf("replacement stole backlog=%+v %v", other, err)
	}
	if stats, err := fx.Store.QueueStateForBinding(fx.Ctx, fx.App.ID, created.Binding.ID); err != nil || stats.Depth != 0 || stats.DeadLetter != 0 {
		t.Fatalf("foreign binding stats=%+v %v", stats, err)
	}
	for _, bad := range []string{"", "UPPER", "staging/other"} {
		if _, err := fx.Store.QueueStateForBindingInScope(fx.Ctx, app.ID, created.Binding.ID, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("invalid scope accepted=%v", err)
		}
	}
	policy := workpolicy.Policy{Name: "ordered", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest}
	keyedRequest := state.Invocation{ID: uuid.NewString(), AppID: app.ID, AccountID: fx.Account.ID, Source: state.InvocationQueue, QueueName: "payments", DeploymentScope: "default", DueAt: time.Now()}
	keyed, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, keyedRequest, policy, "s:key")
	if err != nil || keyed.QueueBindingID != created.Binding.ID {
		t.Fatalf("keyed identity=%+v %v", keyed, err)
	}
	keyedRequest.QueueBindingID = replacement.ID
	if _, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, keyedRequest, policy, "s:key"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("idempotent work moved owner: %v", err)
	}
	for _, bad := range []state.Invocation{
		{AppID: app.ID, AccountID: fx.Account.ID, Source: state.InvocationAsyncInvoke, QueueBindingID: created.Binding.ID},
		{AppID: fx.App.ID, AccountID: fx.Account.ID, Source: state.InvocationQueue, QueueBindingID: created.Binding.ID},
		{AppID: app.ID, AccountID: uuid.NewString(), Source: state.InvocationQueue, QueueBindingID: created.Binding.ID},
		{AppID: app.ID, AccountID: fx.Account.ID, Source: state.InvocationQueue, QueueBindingID: "malformed"},
	} {
		if _, err := fx.Store.EnqueueInvocation(fx.Ctx, bad); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("forged queue identity accepted: %v", err)
		}
	}
	if err := fx.Store.DeleteQueueBinding(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := fx.Store.RetryQueueDeadLetter(fx.Ctx, fx.Account.ID, old.ID)
	if err != nil || replayed.QueueBindingID != created.Binding.ID || replayed.QueueName != "orders" {
		t.Fatalf("rename replay changed admission=%+v %v", replayed, err)
	}
	for _, inv := range []state.Invocation{old, unnamed, neighbor, fresh, keyed} {
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingRetired) {
			t.Fatalf("old label escaped retired UID hold: %v", err)
		}
	}
	// A rejected keyed producer must not supersede the retained pending row.
	keyedRequest.ID = uuid.NewString()
	keyedRequest.QueueBindingID = created.Binding.ID
	if _, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, keyedRequest, policy, "s:key"); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("retired keyed admission=%v", err)
	}
	if row, err := fx.Store.InvocationByID(fx.Ctx, keyed.ID); err != nil || row.State != state.InvocationPending {
		t.Fatalf("rejected producer superseded work=%+v %v", row, err)
	}
	if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, replacementWork.ID, "", 60, 10); err != nil {
		t.Fatalf("retirement held replacement's work: %v", err)
	}
	if id, err := fx.Store.TriggerRecordIDByItemIdentifier(fx.Ctx, created.Changes[0].TriggerID, old.ID); err != nil || id != receipt {
		t.Fatalf("rename/retire lost receipt=%q %v", id, err)
	}
}
