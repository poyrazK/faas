package sched_test

// adr: 20261029012000

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// A backlog larger than one sweep's budget drains from a single wake: each
// pass that uses its whole budget asks for the next one at once, instead of
// leaving the rest for the next wake or the 5 s safety tick.
func TestEventFanoutBacklogDrainsFromOneWake(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	account, err := store.CreateAccount(ctx, "fanout-backlog-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "fanout-backlog",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "order.created", nil); err != nil {
		t.Fatal(err)
	}
	const backlog = 250 // two and a half sweeps' budget
	for i := range backlog {
		envelope, err := (events.Envelope{ID: fmt.Sprintf("evt-backlog-%d", i), Source: "orders", Type: "order.created",
			Data: json.RawMessage(`{}`)}).Normalize(account.ID, time.Now().UTC())
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
	loop.XDispatchEventFanoutSweep(ctx)
	loop.WorkPool().Drain()

	if left, err := store.ClaimDuePublishedEventsForRecipientAdoption(ctx, time.Now().UTC(), backlog); err != nil || len(left) != 0 {
		t.Fatalf("receipts left after one wake = %d, %v; want none", len(left), err)
	}
	if _, err := store.ClaimDuePublishedEventRecipient(ctx, time.Now().UTC()); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a recipient was left unrouted after one wake: %v", err)
	}
}
