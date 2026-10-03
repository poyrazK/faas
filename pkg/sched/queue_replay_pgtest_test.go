// adr: 493 — environment intent and runtime ownership contracts.
package sched

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func seedQueueReplay(t *testing.T) (*pgxpool.Pool, *state.PgStore, state.QueueBinding, sqlc.Trigger, state.Invocation) {
	t.Helper()
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "queue-recovery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-recovery", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "jobs", QueueName: "jobs", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.TriggerByID(ctx, binding.Changes[0].TriggerID)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: "jobs", DeploymentScope: "staging", Payload: []byte(`{"job":"original"}`), DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return pool, store, binding.Binding, trigger, inv
}

// Qualifies durable receipt admission, HTTP transport and acknowledgement.
// Guest execution is represented by the HTTP handler; native acceptance is separate.
func TestQueueReplayDispatchesOriginalConsumerAfterRename(t *testing.T) {
	pool, store, binding, trigger, inv := seedQueueReplay(t)
	ctx := t.Context()
	calls := 0
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope triggerDispatchRequest
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil || len(envelope.Records) != 1 {
			t.Errorf("recovery envelope: %+v %v", envelope, err)
			w.WriteHeader(400)
			return
		}
		record := envelope.Records[0]
		if record.InvocationID != inv.ID || record.InvocationAttempt != 1 || record.InvocationReplayGeneration != int64(calls) || envelope.TriggerID != trigger.ID.String() {
			t.Errorf("recovery identity: %+v", record)
			w.WriteHeader(400)
			return
		}
		admitted, err := state.AdmitPlatformTenantInvocation(r.Context(), store, inv.AppID, state.Invocation{ID: record.InvocationID, Source: "esm", Attempts: record.InvocationAttempt, ReplayGeneration: record.InvocationReplayGeneration})
		if err != nil || admitted.DeploymentScope != "staging" {
			t.Errorf("recovery admission: %+v %v", admitted, err)
			w.WriteHeader(400)
			return
		}
		status := "dead_letter"
		if calls > 1 {
			status = "succeeded"
		}
		calls++
		_ = json.NewEncoder(w).Encode(triggerDispatchResponse{Results: []triggerDispatchResult{{ItemIdentifier: inv.ID, Status: status, Code: "poison_record", Error: "delivery failed"}}})
	}))
	defer gateway.Close()
	loop := makeLoopForDLQ()
	loop.pool = pool
	loop.gatewayHTTPClient, loop.gatewayBaseURL = gateway.Client(), gateway.URL
	t.Cleanup(func() {
		for _, poller := range loop.triggerPollers {
			_ = poller.Close()
		}
	})
	tick := func(tg sqlc.Trigger) {
		t.Helper()
		if err := loop.dispatchOneTrigger(ctx, tg, store, func(string) api.Plan { return api.PlanPro }); err != nil {
			t.Fatal(err)
		}
	}
	tick(trigger)
	if row, err := store.InvocationByID(ctx, inv.ID); err != nil || row.State != state.InvocationDeadLetter || calls != 1 {
		t.Fatalf("original delivery=%+v calls=%d %v", row, calls, err)
	}
	receiptID, err := store.TriggerRecordIDByItemIdentifier(ctx, trigger.ID.String(), inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	name, disabled, enabled := "renamed-jobs", false, true
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, inv.AccountID, inv.AppID, binding.ID, state.UpdateQueueBindingParams{QueueName: &name, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	replacement, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: inv.AccountID, AppID: inv.AppID, Name: "replacement", QueueName: "jobs", Mode: "pull", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	neighbor, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: inv.AccountID, AppID: inv.AppID, Source: state.InvocationQueue, QueueName: "jobs", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryQueueDeadLetter(ctx, inv.AccountID, inv.ID); err != nil {
		t.Fatal(err)
	}
	held, err := store.TriggerByID(ctx, trigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	tick(held)
	if calls != 1 {
		t.Fatal("replay escaped disabled consumer")
	}
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, inv.AccountID, inv.AppID, binding.ID, state.UpdateQueueBindingParams{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	current, err := store.TriggerByID(ctx, trigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	tick(current)
	if row, err := store.InvocationByID(ctx, inv.ID); err != nil || calls != 2 || row.State != state.InvocationDeadLetter {
		t.Fatalf("second failure=%+v calls=%d %v", row, calls, err)
	}
	if _, err := store.RetryQueueDeadLetter(ctx, inv.AccountID, inv.ID); err != nil {
		t.Fatal(err)
	}
	tick(current)
	row, err := store.InvocationByID(ctx, inv.ID)
	if err != nil || calls != 3 || row.State != state.InvocationCompleted || row.QueueBindingID != binding.ID || row.QueueName != "jobs" || row.ReplayGeneration != 2 || row.DeploymentScope != "staging" {
		t.Fatalf("redelivery=%+v calls=%d %v", row, calls, err)
	}
	var receiptState string
	if err := pool.QueryRow(ctx, `select state from trigger_records where id=$1 and trigger_id=$2 and item_identifier=$3`, receiptID, trigger.ID, inv.ID).Scan(&receiptState); err != nil || receiptState != "succeeded" {
		t.Fatalf("original receipt not completed=%q %v", receiptState, err)
	}
	if audit, err := store.ListTriggerDeadLetter(ctx, trigger.ID.String(), 10); err != nil || len(audit) != 1 || !json.Valid(audit[0].FailureHistory) || string(audit[0].FailureHistory) == "[]" {
		t.Fatalf("replay erased failure audit=%+v %v", audit, err)
	}
	if row, err := store.InvocationByID(ctx, neighbor.ID); err != nil || row.State != state.InvocationPending || row.QueueBindingID != replacement.ID || row.Attempts != 0 {
		t.Fatalf("replay stole replacement work=%+v %v", row, err)
	}
}

func TestQueueReplayFencesPriorPollerHandles(t *testing.T) {
	pool, store, _, trigger, inv := seedQueueReplay(t)
	ctx := t.Context()
	source, err := newQueuePoller(pool, trigger, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := source.(*queuePoller)
	first := old.Poll(ctx, trigger)
	if first.Error != nil || len(first.Records) != 1 {
		t.Fatalf("first claim=%+v", first)
	}
	if err := store.FailInvocation(ctx, inv.ID, "operator failure", time.Nanosecond, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RetryQueueDeadLetter(ctx, inv.AccountID, inv.ID); err != nil {
		t.Fatal(err)
	}
	oldDelivery := old.deliveryPoller(first.Records)
	second := old.Poll(ctx, trigger)
	if second.Error != nil || len(second.Records) != 1 || second.Records[0].InvocationAttempt != first.Records[0].InvocationAttempt || second.Records[0].InvocationReplayGeneration != 1 {
		t.Fatalf("replay claim=%+v", second)
	}
	current := old.deliveryPoller(second.Records)
	if err := oldDelivery.Ack(ctx, trigger, []string{inv.ID}); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	if old.itemsInFlight[inv.ID].ReplayGeneration != 1 {
		t.Fatal("old batch cleared the recovered delivery handle")
	}
	for _, operation := range []string{"ack", "nack", "release"} {
		staleSource, err := newQueuePoller(pool, trigger, nil)
		if err != nil {
			t.Fatal(err)
		}
		stale := staleSource.(*queuePoller)
		stale.itemsInFlight[inv.ID] = queueDeliveryClaim{Attempt: 1, ReplayGeneration: 0}
		switch operation {
		case "ack":
			err = stale.Ack(ctx, trigger, []string{inv.ID})
		case "nack":
			err = stale.Nack(ctx, trigger, []string{inv.ID}, triggerReasonPoisonRecord)
		case "release":
			err = stale.releaseNamedClaims(ctx, map[string]queueDeliveryClaim{inv.ID: {Attempt: 1, ReplayGeneration: 0}}, trigger)
		}
		if err != nil {
			t.Fatal(err)
		}
		if row, err := store.InvocationByID(ctx, inv.ID); err != nil || row.State != state.InvocationDispatching || row.ReplayGeneration != 1 || row.Attempts != 1 {
			t.Fatalf("stale %s changed replay=%+v %v", operation, row, err)
		}
	}
	if _, err := state.AdmitPlatformTenantInvocation(ctx, store, inv.AppID, state.Invocation{ID: inv.ID, Source: "esm", Attempts: 1}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old HTTP envelope admitted=%v", err)
	}
	if err := current.Ack(ctx, trigger, []string{inv.ID}); err != nil {
		t.Fatal(err)
	}
	if row, err := store.InvocationByID(ctx, inv.ID); err != nil || row.State != state.InvocationCompleted {
		t.Fatalf("current completion=%+v %v", row, err)
	}
}
