package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQueueBindingStatusReportsProjectionAndQueueState(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := (*e.Store()).CreateApp(ctx, state.App{
		AccountID: (*e.Acct()).ID, Slug: "queue-status", WorkloadClass: state.WorkloadClassWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (*e.Store()).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{
		AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "orders",
		QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker,
		Enabled: true, MaxConcurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	binding := result.Binding
	triggerID, err := apidserver.QueueBindingTriggerID(ctx, (*e.Store()), app.ID, binding.ID)
	if err != nil || triggerID == "" {
		t.Fatalf("load projected trigger: id=%q err=%v", triggerID, err)
	}
	lagMessages := int64(7)
	lagAge := 2.5
	lastPoll := time.Now().UTC()
	if err := (*e.Store()).RecordTriggerConsumerHealth(ctx, triggerID, state.TriggerConsumerHealthObservation{
		LastPollAt: lastPoll, Success: true, LagMessages: &lagMessages, LagAgeSeconds: &lagAge,
	}); err != nil {
		t.Fatalf("record consumer health: %v", err)
	}
	if _, err := (*e.Store()).EnqueueInvocation(ctx, state.Invocation{
		AccountID: (*e.Acct()).ID, AppID: app.ID, QueueName: "orders", Source: state.InvocationQueue,
		Payload: []byte(`{"order":"123"}`), DueAt: time.Now().UTC().Add(-time.Minute),
		CreatedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed queue row: %v", err)
	}

	rec := e.Do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/"+binding.ID+"/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got api.QueueBindingStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ConsumerState != "active" || got.ConsumerStateReason != "push_consumer_enabled" {
		t.Fatalf("consumer state = %q/%q, want active/push_consumer_enabled", got.ConsumerState, got.ConsumerStateReason)
	}
	if got.ConsumerLiveness != "healthy" || got.LastPollAt == nil || got.LastSuccessAt == nil {
		t.Fatalf("consumer liveness = %q poll=%v success=%v, want healthy with timestamps", got.ConsumerLiveness, got.LastPollAt, got.LastSuccessAt)
	}
	if got.LagMessages == nil || *got.LagMessages != lagMessages || got.LagAgeSeconds == nil || *got.LagAgeSeconds != lagAge {
		t.Fatalf("consumer lag = messages=%v age=%v, want %d/%v", got.LagMessages, got.LagAgeSeconds, lagMessages, lagAge)
	}
	if got.TriggerID == "" {
		t.Fatal("trigger_id is empty for a projected push consumer")
	}
	if got.Depth != 1 || got.InFlight != 0 || got.DeadLetter != 0 {
		t.Fatalf("queue counters = depth=%d in_flight=%d dead_letter=%d, want 1/0/0", got.Depth, got.InFlight, got.DeadLetter)
	}
	if got.OldestPendingAt == nil || got.OldestPendingAgeSeconds == nil {
		t.Fatal("oldest pending fields are missing")
	}

	// Renaming keeps the accepted queue ledger visible through binding identity.
	name := "payments"
	result, err = (*e.Store()).UpdateQueueBindingWithConsumer(ctx, (*e.Acct()).ID, app.ID, binding.ID, state.UpdateQueueBindingParams{QueueName: &name})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := (*e.Store()).CreateQueueBinding(ctx, state.QueueBinding{AccountID: (*e.Acct()).ID, AppID: app.ID, Name: "replacement", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		id    string
		depth int
	}{{binding.ID, 1}, {replacement.ID, 0}} {
		rec = e.Do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/"+item.id+"/status", nil, nil)
		var status api.QueueBindingStatusResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &status) != nil || status.Depth != item.depth {
			t.Fatalf("renamed status depth=%d want=%d code=%d", status.Depth, item.depth, rec.Code)
		}
	}

	disabled := false
	result, err = (*e.Store()).UpdateQueueBindingWithConsumer(ctx, (*e.Acct()).ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Enabled: &disabled})
	if err != nil {
		t.Fatalf("disable binding: %v", err)
	}
	binding = result.Binding
	rec = e.Do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/"+binding.ID+"/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("disabled status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got = api.QueueBindingStatusResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode disabled status: %v", err)
	}
	if got.ConsumerState != "paused" || got.ConsumerStateReason != "push_consumer_disabled" {
		t.Fatalf("disabled consumer state = %q/%q, want paused/push_consumer_disabled", got.ConsumerState, got.ConsumerStateReason)
	}
	if got.ConsumerLiveness != "not_observed" {
		t.Fatalf("disabled consumer liveness = %q, want not_observed", got.ConsumerLiveness)
	}

	pull := "pull"
	result, err = (*e.Store()).UpdateQueueBindingWithConsumer(ctx, (*e.Acct()).ID, app.ID, binding.ID, state.UpdateQueueBindingParams{Mode: &pull})
	if err != nil {
		t.Fatalf("switch binding to pull: %v", err)
	}
	binding = result.Binding
	rec = e.Do(t, http.MethodGet, "/v1/apps/queue-status/queue-bindings/"+binding.ID+"/status", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	got = api.QueueBindingStatusResponse{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode pull status: %v", err)
	}
	if got.ConsumerState != "external" || got.ConsumerStateReason != "" || got.TriggerID != "" {
		t.Fatalf("pull consumer state = %q/%q trigger=%q, want external/no reason/no trigger", got.ConsumerState, got.ConsumerStateReason, got.TriggerID)
	}
}

func TestQueueBindingConsumerLivenessClassifiesErrorsAndStaleness(t *testing.T) {
	now := time.Now().UTC()
	poll := now.Add(-time.Second)
	errAt := now.Add(-500 * time.Millisecond)
	if got := apidserver.QueueBindingConsumerLiveness(now, state.TriggerConsumerHealth{LastPollAt: &poll, LastErrorAt: &errAt}); got != "degraded" {
		t.Fatalf("degraded liveness = %q, want degraded", got)
	}
	stale := now.Add(-api.QueueConsumerMaxPollAge - time.Second)
	if got := apidserver.QueueBindingConsumerLiveness(now, state.TriggerConsumerHealth{LastPollAt: &stale}); got != "stale" {
		t.Fatalf("stale liveness = %q, want stale", got)
	}
	if got := apidserver.QueueBindingConsumerLiveness(now, state.TriggerConsumerHealth{}); got != "not_observed" {
		t.Fatalf("empty liveness = %q, want not_observed", got)
	}
	zero, future := time.Time{}, now.Add(time.Second)
	if got := apidserver.QueueBindingConsumerLiveness(now, state.TriggerConsumerHealth{LastPollAt: &zero}); got != "not_observed" {
		t.Fatalf("zero poll liveness = %q, want not_observed", got)
	}
	if got := apidserver.QueueBindingConsumerLiveness(now, state.TriggerConsumerHealth{LastPollAt: &future}); got != "unknown" {
		t.Fatalf("future poll liveness = %q, want unknown", got)
	}
	boundary := now.Add(-api.QueueConsumerMaxPollAge)
	if got := apidserver.QueueBindingConsumerLiveness(now, state.TriggerConsumerHealth{LastPollAt: &boundary}); got != "healthy" {
		t.Fatalf("poll age boundary = %q, want healthy", got)
	}
}
