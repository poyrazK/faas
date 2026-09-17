// adr: 179 — deadline failures must reach the configured failure destination.
package sched

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInvocationsRetentionDeadlineEmitsFailureDestination(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	account, app, _ := seedApp(t, store, api.PlanPro, 512, 5)

	hook, err := store.CreateAppWebhook(ctx, state.AppWebhook{
		AccountID:    account.ID,
		AppID:        app.ID,
		TargetURL:    "https://example.com/deadline",
		SecretSealed: []byte("sealed"),
		EventFilter:  []string{string(state.AppWebhookEventJobFinished)},
		RetryPolicy:  state.AppWebhookRetryDefault,
		Enabled:      true,
	})
	if err != nil {
		t.Fatalf("CreateAppWebhook: %v", err)
	}

	now := time.Date(2026, 9, 17, 19, 0, 0, 0, time.UTC)
	deadline := now.Add(-time.Minute)
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{
		AccountID:              account.ID,
		AppID:                  app.ID,
		Source:                 state.InvocationAsyncInvoke,
		State:                  state.InvocationPending,
		Method:                 "POST",
		Path:                   "/invoke",
		DueAt:                  now.Add(-2 * time.Minute),
		DeadlineAt:             &deadline,
		Attempts:               3,
		OnFailureDestinationID: hook.ID,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}

	retention := NewInvocationsRetention(store, slog.Default()).WithClock(func() time.Time { return now })
	forced, err := retention.SweepDeadlineBreached(ctx, 10)
	if err != nil {
		t.Fatalf("SweepDeadlineBreached: %v", err)
	}
	if forced != 1 {
		t.Fatalf("forced = %d, want 1", forced)
	}
	got, err := store.InvocationByID(ctx, inv.ID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if got.State != state.InvocationDeadLetter || got.Outcome == nil || *got.Outcome != state.OutcomeTimeout {
		t.Fatalf("terminal invocation = %+v, want dead_letter/timeout", got)
	}

	deliveries, _, err := store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil {
		t.Fatalf("ListAppWebhookDeliveries: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries))
	}
	var payload map[string]any
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatalf("decode delivery payload: %v", err)
	}
	if payload["status"] != string(state.OutcomeTimeout) || payload["outcome"] != string(state.OutcomeTimeout) {
		t.Fatalf("delivery payload = %v, want timeout status/outcome", payload)
	}
	if payload["job_id"] != inv.ID || payload["source"] != string(state.InvocationAsyncInvoke) {
		t.Fatalf("delivery identity = %v, want invocation %s/%s", payload, inv.ID, state.InvocationAsyncInvoke)
	}

	forced, err = retention.SweepDeadlineBreached(ctx, 10)
	if err != nil {
		t.Fatalf("second SweepDeadlineBreached: %v", err)
	}
	if forced != 0 {
		t.Fatalf("second forced = %d, want 0", forced)
	}
	deliveries, _, err = store.ListAppWebhookDeliveries(ctx, app.ID, hook.ID, 10, "")
	if err != nil {
		t.Fatalf("ListAppWebhookDeliveries after second sweep: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("deliveries after second sweep = %d, want 1", len(deliveries))
	}
}
