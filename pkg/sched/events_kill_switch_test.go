// adr: 20261027125625 — the events flag holds event routing: everything while
// its kill switch is off, an account's customer events while it is off for
// that account. Held work keeps its routing attempts.
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
	"github.com/onebox-faas/faas/pkg/capflags"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

type eventsSwitchFixture struct {
	store   *state.MemStore
	account state.Account
	app     state.App
	sub     state.EventSubscription
}

func newEventsSwitchFixture(t *testing.T, sources ...string) eventsSwitchFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "events-switch-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "events-switch",
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	f := eventsSwitchFixture{store: store, account: account, app: app}
	for _, source := range sources {
		sub, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, source, "order.created", nil)
		if err != nil {
			t.Fatal(err)
		}
		if f.sub.ID == "" {
			f.sub = sub
		}
	}
	return f
}

func (f eventsSwitchFixture) publish(t *testing.T, id, source string) {
	t.Helper()
	envelope, err := (events.Envelope{ID: id, Source: source, Type: "order.created",
		Data: json.RawMessage(`{"order_id":"o-1"}`)}).Normalize(f.account.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.AppendEvent(context.Background(), "apid", "event.published", &f.account.ID, payload); err != nil {
		t.Fatal(err)
	}
}

func (f eventsSwitchFixture) invocations(t *testing.T) int {
	t.Helper()
	got, err := f.store.ListInvocationsForApp(context.Background(), f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	return len(got)
}

func eventsDecider(t *testing.T, def platformflags.Definition) *capflags.Decider {
	t.Helper()
	key, ok := platformflags.CapabilityFlag("events")
	if !ok {
		t.Fatal("events has no platform flag")
	}
	set := platformflags.NewSet(map[string]platformflags.Definition{key: def})
	decider, err := capflags.Load(set.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return decider
}

func eventsSwitchLoop(t *testing.T, store *state.MemStore, decider *capflags.Decider, recipientClaims bool) *sched.Loop {
	t.Helper()
	engine := sched.XNewEngine(t, store, &sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	engine.WithCapabilities(decider)
	return sched.NewLoop(nil, engine, slog.New(slog.NewTextHandler(io.Discard, nil))).WithEventRecipientClaims(recipientClaims)
}

// With the kill switch off schedd claims nothing, so the receipt keeps every
// attempt; turning it back on routes the event.
func TestEventFanoutHoldsAllRoutingWhileEventsIsKilled(t *testing.T) {
	for _, claims := range []bool{true, false} {
		f := newEventsSwitchFixture(t, "orders")
		f.publish(t, "evt-killed", "orders")
		ctx := context.Background()

		eventsSwitchLoop(t, f.store, eventsDecider(t, platformflags.Definition{Enabled: false}), claims).XRunEventFanoutSweep(ctx)
		if n := f.invocations(t); n != 0 {
			t.Fatalf("claims=%v: invocations while events is killed = %d, want 0", claims, n)
		}

		eventsSwitchLoop(t, f.store, eventsDecider(t, platformflags.Definition{Enabled: true, Default: true}), claims).XRunEventFanoutSweep(ctx)
		if n := f.invocations(t); n != 1 {
			t.Fatalf("claims=%v: invocations after events is back on = %d, want 1", claims, n)
		}
	}
}

// Off for one account, its customer event waits without spending a routing
// attempt, while a platform-sourced event to the same app still routes.
func TestEventFanoutHoldsAnAccountsCustomerEvents(t *testing.T) {
	for _, claims := range []bool{true, false} {
		f := newEventsSwitchFixture(t, "orders", "gregale.storage")
		f.publish(t, "evt-held", "orders")
		f.publish(t, "evt-platform", "gregale.storage")
		ctx := context.Background()
		// The kill switch is on; events is off by default, as for a preview
		// account without an allow rule.
		off := eventsDecider(t, platformflags.Definition{Enabled: true})

		eventsSwitchLoop(t, f.store, off, claims).XRunEventFanoutSweep(ctx)
		if n := f.invocations(t); n != 1 {
			t.Fatalf("claims=%v: invocations = %d, want only the platform event's", claims, n)
		}
		later := time.Now().UTC().Add(2 * time.Minute)
		if claims {
			held, err := f.store.ClaimDuePublishedEventRecipients(ctx, later, 10, true)
			if err != nil || len(held) != 1 || held[0].TotalAttempts != 1 ||
				held[0].PreviousProgress.DeliveryControlReason != state.EventCapabilityWaitReason {
				t.Fatalf("held recipient = %+v, %v; want one waiting with its attempt unspent", held, err)
			}
			if err := f.store.ReleasePublishedEventRecipientClaim(ctx, held[0]); err != nil {
				t.Fatal(err)
			}
		} else {
			receipt, err := f.store.ClaimDuePublishedEvent(ctx, later)
			if err != nil {
				t.Fatal(err)
			}
			progress := receipt.RecipientProgress[f.sub.ID]
			if progress.Attempts != 0 || progress.DeliveryControlReason != state.EventCapabilityWaitReason {
				t.Fatalf("held legacy progress = %+v; want a capability wait with no attempt spent", progress)
			}
			if err := f.store.FinishPublishedEvent(ctx, receipt.ID, receipt.ClaimToken, state.ErrEventDeliveryCapacity); err != nil {
				t.Fatal(err)
			}
		}

		allow := true
		on := eventsDecider(t, platformflags.Definition{Enabled: true,
			Rules: []platformflags.Rule{{ID: "partner", Accounts: []string{f.account.ID}, Value: &allow}}})
		loop := eventsSwitchLoop(t, f.store, on, claims)
		for i := 0; i < 3 && f.invocations(t) < 2; i++ {
			loop.XRunEventFanoutSweepAt(ctx, later.Add(time.Duration(i)*2*time.Minute))
		}
		if n := f.invocations(t); n != 2 {
			t.Fatalf("claims=%v: invocations after the account is released = %d, want 2", claims, n)
		}
	}
}
