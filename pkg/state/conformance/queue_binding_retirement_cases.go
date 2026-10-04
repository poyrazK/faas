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

func testQueueBindingRetirement(t *testing.T, fx *Fixture) {
	if err := fx.Store.UpdateAccountPlan(fx.Ctx, fx.Account.ID, api.PlanHobby); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(api.PlanHobby)
	store := fx.Store.(state.QueueBindingConsumerStore)
	app, err := fx.Store.CreateApp(fx.Ctx, state.App{AccountID: fx.Account.ID, Slug: "retired-" + uuid.NewString()[:8], Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	request := state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2}
	created, err := store.CreateQueueBindingWithConsumer(fx.Ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	triggerID := created.Changes[0].TriggerID
	receiptID, err := fx.Store.InsertTriggerRecord(fx.Ctx, triggerID, "receipt", []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	enqueue := func() state.Invocation {
		t.Helper()
		inv, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AccountID: fx.Account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", Payload: []byte(`{}`), DueAt: time.Now().Add(-time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		return inv
	}
	pending, inflight, dead := enqueue(), enqueue(), enqueue()
	for _, inv := range []state.Invocation{inflight, dead} {
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := fx.Store.FailInvocation(fx.Ctx, dead.ID, "exhausted", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
	key, err := workpolicy.CanonicalScalar([]byte(`"order-1"`))
	if err != nil {
		t.Fatal(err)
	}
	keyed, err := fx.Store.EnqueueKeyedInvocation(fx.Ctx, state.Invocation{AccountID: fx.Account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now()}, workpolicy.Policy{Name: "ordered", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingAll}, key)
	if err != nil {
		t.Fatal(err)
	}
	// Pull mode retains delivery identity while freeing a consumer quota slot.
	pull, push := "pull", "push"
	changed, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Mode: &pull})
	if err != nil || len(changed.Changes) != 1 || changed.Changes[0].TriggerID != triggerID {
		t.Fatalf("pull identity: %+v %v", changed.Changes, err)
	}
	if records, err := fx.Store.ClaimTriggerRecords(fx.Ctx, triggerID, 10); err != nil || len(records) != 0 {
		t.Fatalf("held receipt claimed: %d %v", len(records), err)
	}
	addTrigger := func() (string, error) {
		row, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, app.ID, "nats", uuid.NewString(), false, []byte(`{}`), "", 1, 1000, 3, 1024, "commit", limits)
		return row.ID.String(), err
	}
	fillers := []string{}
	for range limits.TriggerLimitPerApp {
		id, err := addTrigger()
		if err != nil {
			t.Fatalf("held consumer consumed quota: %v", err)
		}
		fillers = append(fillers, id)
	}
	var quota *state.TriggerQuotaError
	if _, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Mode: &push}); !errors.As(err, &quota) {
		t.Fatalf("restored consumer bypassed quota: %v", err)
	}
	if consumer, err := fx.Store.TriggerByID(fx.Ctx, triggerID); err != nil || consumer.Enabled {
		t.Fatalf("failed activation changed consumer: %v %v", consumer.Enabled, err)
	}
	if err := fx.Store.DeleteTrigger(fx.Ctx, fillers[0], app.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Mode: &push})
	if err != nil || len(restored.Changes) != 1 || restored.Changes[0].TriggerID != triggerID {
		t.Fatalf("restored identity: %+v %v", restored.Changes, err)
	}
	if id, err := fx.Store.TriggerRecordIDByItemIdentifier(fx.Ctx, triggerID, "receipt"); err != nil || id != receiptID {
		t.Fatalf("pull/push lost receipt: %q %v", id, err)
	}
	// The legacy deletion seam must retire the same private projection.
	if err := fx.Store.DeleteQueueBinding(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID); err != nil {
		t.Fatal(err)
	}
	history, err := fx.Store.QueueBindingHistoryByID(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID)
	if err != nil || history.RetiredAt == nil || history.Enabled {
		t.Fatalf("retained binding: %+v %v", history, err)
	}
	if _, err := fx.Store.QueueBindingHistoryByID(fx.Ctx, uuid.NewString(), app.ID, created.Binding.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign history exposed: %v", err)
	}
	if active, err := fx.Store.ListQueueBindingsForApp(fx.Ctx, fx.Account.ID, app.ID); err != nil || len(active) != 0 {
		t.Fatalf("retired binding remained active: %d %v", len(active), err)
	}
	if rows, err := fx.Store.ListQueueBindingHistoryForApp(fx.Ctx, fx.Account.ID, app.ID); err != nil || len(rows) != 1 || rows[0].RetiredAt == nil {
		t.Fatalf("missing history: %d %v", len(rows), err)
	}
	if _, err := store.UpdateQueueBindingWithConsumer(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{Mode: &push}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ordinary patch resurrected binding: %v", err)
	}
	request.Name = "replacement"
	if _, err := store.CreateQueueBindingWithConsumer(fx.Ctx, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retained queue name reused: %v", err)
	}
	request.ID = created.Binding.ID
	request.QueueName = "another"
	if _, err := fx.Store.CreateQueueBinding(fx.Ctx, request); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("retired ID reused: %v", err)
	}
	if records, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, triggerID, []string{"receipt"}); err != nil || len(records) != 0 {
		t.Fatalf("retired receipt claimed: %d %v", len(records), err)
	}
	for _, inv := range []state.Invocation{pending, keyed} {
		if _, err := fx.Store.ClaimInvocationWithCap(fx.Ctx, inv.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingRetired) {
			t.Fatalf("retired capped claim: %v", err)
		}
		if row, err := fx.Store.InvocationByID(fx.Ctx, inv.ID); err != nil || row.State != state.InvocationPending || row.Attempts != 0 {
			t.Fatalf("retired claim changed work: %+v %v", row, err)
		}
	}
	if _, err := fx.Store.ClaimInvocation(fx.Ctx, pending.ID, "", 60); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("retired direct claim: %v", err)
	}
	if _, err := fx.Store.EnqueueInvocation(fx.Ctx, state.Invocation{AccountID: fx.Account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", DueAt: time.Now()}); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("retired queue accepted new work: %v", err)
	}
	replayed, err := fx.Store.RetryQueueDeadLetter(fx.Ctx, fx.Account.ID, dead.ID)
	if err != nil || replayed.ID != dead.ID || replayed.QueueName != "orders" || replayed.State != state.InvocationPending {
		t.Fatalf("retained replay: %+v %v", replayed, err)
	}
	if _, err := fx.Store.ClaimInvocation(fx.Ctx, dead.ID, "", 60); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("replay escaped hold: %v", err)
	}
	if err := fx.Store.CompleteInvocation(fx.Ctx, inflight.ID, []byte(`{}`)); err != nil {
		t.Fatalf("existing delivery could not finish: %v", err)
	}
	_, current, err := fx.Store.GetAccountAsyncQuota(fx.Ctx, fx.Account.ID)
	if err != nil || current != 0 {
		t.Fatalf("held claim leaked account capacity: %d %v", current, err)
	}
	if id, err := fx.Store.TriggerRecordIDByItemIdentifier(fx.Ctx, triggerID, "receipt"); err != nil || id != receiptID {
		t.Fatalf("retirement lost receipt: %q %v", id, err)
	}
	unmanaged, err := fx.Store.CreateQueueBinding(fx.Ctx, state.QueueBinding{AccountID: fx.Account.ID, AppID: app.ID, Name: "unmanaged", QueueName: "unmanaged", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatalf("retirement affected unrelated binding: %v", err)
	}
	maxConcurrency := 3
	updated, err := fx.Store.UpdateQueueBinding(fx.Ctx, fx.Account.ID, app.ID, unmanaged.ID, state.UpdateQueueBindingParams{MaxConcurrency: &maxConcurrency})
	if err != nil || updated.MaxConcurrency != 3 {
		t.Fatalf("legacy binding update: %+v %v", updated, err)
	}
	if _, err := fx.Store.UpdateQueueBinding(fx.Ctx, fx.Account.ID, app.ID, created.Binding.ID, state.UpdateQueueBindingParams{MaxConcurrency: &maxConcurrency}); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("legacy update resurrected retirement: %v", err)
	}
	if _, err := addTrigger(); err != nil {
		t.Fatalf("retired consumer consumed quota: %v", err)
	}
	if _, err := addTrigger(); !errors.As(err, &quota) {
		t.Fatalf("retirement broadened quota: %v", err)
	}
}
