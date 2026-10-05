package sched

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 586
func TestDrainEventAttemptHistoryAndRetention(t *testing.T) {
	d, harnessStore, _, _, gateway := newDrainHarness(t, api.PlanHobby, true)
	store := harnessStore.(*state.MemStore)
	ctx := context.Background()
	apps, err := store.ListAllApps(ctx)
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps: %+v %v", apps, err)
	}
	app := apps[0]
	sub, _, err := store.UpsertEventSubscription(ctx, app.AccountID, app.ID, "orders", "order.created", nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := (events.Envelope{ID: "attempt-history", Source: "orders", Type: "order.created", Data: json.RawMessage(`{"order":1}`)}).Normalize(app.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &app.AccountID, payload); err != nil {
		t.Fatal(err)
	}
	id := state.PublishedEventInvocationID(app.AccountID, envelope.Source, envelope.ID, sub.ID)
	if _, err := store.EnqueueInvocation(ctx, state.Invocation{ID: id, AccountID: app.AccountID, AppID: app.ID, Source: state.InvocationAsyncInvoke, DueAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	d.Tick(ctx)
	h, err := store.EventReceiptAttempts(ctx, app.AccountID, envelope.Source, envelope.ID, sub.ID, state.EventReceiptAttemptCursor{}, 100)
	if err != nil || len(h.Attempts) != 1 || h.Attempts[0].Outcome != "succeeded" || gateway.calls.Load() != 1 {
		t.Fatalf("drain attempt: %+v %v", h, err)
	}
	retention := NewInvocationsRetention(store, nil).WithClock(func() time.Time { return time.Now().Add(31 * 24 * time.Hour) })
	if _, _, err := retention.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	h, err = store.EventReceiptAttempts(ctx, app.AccountID, envelope.Source, envelope.ID, sub.ID, state.EventReceiptAttemptCursor{}, 100)
	if err != nil || len(h.Attempts) != 0 {
		t.Fatalf("retention tick left evidence: %+v %v", h, err)
	}
	if inv, err := store.InvocationByID(ctx, id); err != nil || inv.State != state.InvocationCompleted {
		t.Fatal("attempt pruning removed execution")
	}
}

func TestDrainEventAttemptSettlementRejectsLostClaim(t *testing.T) {
	_, harnessStore, _, _, _ := newDrainHarness(t, api.PlanHobby, true)
	store := harnessStore.(*state.MemStore)
	ctx := context.Background()
	inv := seedDrainInvocation(t, store, state.InvocationAsyncInvoke)
	old, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequeueExpiredInvocations(ctx, time.Now().Add(2*time.Minute), 100); err != nil {
		t.Fatal(err)
	}
	current, err := store.ClaimInvocationWithCap(ctx, inv.ID, "", 60, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := completeClaimedInvocation(ctx, store, old, nil); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old scheduler completed new claim: %v", err)
	}
	if err := completeClaimedInvocation(ctx, store, current, nil); err != nil {
		t.Fatal(err)
	}
}
