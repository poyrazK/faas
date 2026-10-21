package sched_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 614 — a fanout sweep that stops with a claimed batch of receipts hands
// the unadopted ones back without spending their attempts.
func TestEventFanoutSweepReleasesUnadoptedBatchClaims(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "sweep-adoption-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "sweep-adoption",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"evt-adopt-0", "evt-adopt-1", "evt-adopt-2"} {
		envelope, err := (events.Envelope{ID: id, Source: "orders", Type: "order.created",
			Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(account.ID, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
			t.Fatal(err)
		}
	}

	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	stopped, stop := context.WithCancel(ctx)
	stop()
	loop.XRunEventFanoutSweep(stopped)

	batch, err := store.ClaimDuePublishedEventsForRecipientAdoption(ctx, time.Now().UTC(), 10)
	if err != nil || len(batch) != 3 {
		t.Fatalf("after a stopped sweep, adoptable receipts = %+v, %v; want all three", batch, err)
	}
	for _, work := range batch {
		if work.Attempts != 1 || work.RecipientClaims {
			t.Fatalf("receipt %d attempts = %d, adopted = %v; the stopped sweep spent or adopted it", work.ID, work.Attempts, work.RecipientClaims)
		}
	}
}

// adr: 614 — a fanout sweep that stops with a claimed batch hands the
// unrouted claims back, so a schedd restart does not leave them waiting out
// their five-minute lease or spend their attempts.
func TestEventRecipientSweepReleasesUnroutedBatchClaims(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "sweep-release-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "sweep-release",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []string{"order.created", "*", "order.*"} {
		if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", eventType, nil); err != nil {
			t.Fatal(err)
		}
	}
	envelope, err := (events.Envelope{ID: "evt-release", Source: "orders", Type: "order.created",
		Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(account.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.ClaimDuePublishedEventForRecipientAdoption(ctx, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.InitializePublishedEventRecipients(ctx, receipt, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	loop := sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil)))
	stopped, stop := context.WithCancel(ctx)
	stop()
	loop.XRunEventRecipientSweep(stopped)

	batch, err := store.ClaimDuePublishedEventRecipients(ctx, time.Now().UTC(), 10, true)
	if err != nil || len(batch) != 3 {
		t.Fatalf("after a stopped sweep, claimable deliveries = %+v, %v; want all three", batch, err)
	}
	for _, work := range batch {
		if work.Attempts != 1 || work.TotalAttempts != 1 {
			t.Fatalf("delivery %s attempts = %d/%d; the stopped sweep spent an attempt", work.Recipient.ID, work.Attempts, work.TotalAttempts)
		}
	}
}
