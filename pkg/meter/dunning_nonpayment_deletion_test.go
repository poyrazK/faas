// adr: 20261017125602
// Non-payment deletion commands retain durable notices and follow-up receipts.
package meter_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

type nonpaymentCommandFake struct {
	store              *state.MemStore
	mode               string
	on                 *atomic.Bool
	intent, lease      string
	at                 time.Time
	calls, completions int
	done               bool
}

func (f *nonpaymentCommandFake) Schedule(ctx context.Context, id string, expected time.Time) (api.InternalMailNonpaymentDeletionResponse, error) {
	f.calls++
	if f.mode == "offline" {
		return api.InternalMailNonpaymentDeletionResponse{}, errors.New("offline")
	}
	a, err := f.store.AccountByID(ctx, id)
	if err != nil {
		return api.InternalMailNonpaymentDeletionResponse{}, err
	}
	if a.PastDueAt == nil || !a.PastDueAt.Equal(expected) {
		return api.InternalMailNonpaymentDeletionResponse{}, errors.New("wrong overdue period")
	}
	if a.Status == state.AccountSuspended {
		if err := f.store.MarkDunningStep(ctx, id, state.AccountSuspended, state.AccountDeletedPending); err != nil {
			return api.InternalMailNonpaymentDeletionResponse{}, err
		}
	}
	if f.mode == "lost_reply" && f.calls == 1 {
		return api.InternalMailNonpaymentDeletionResponse{}, errors.New("lost response after commit")
	}
	if f.mode == "withdrawn" {
		f.on.Store(false)
	}
	return api.InternalMailNonpaymentDeletionResponse{IntentID: f.intent, LeaseID: f.lease, DeletionRequestedAt: f.at, Followup: !f.done}, nil
}

func (f *nonpaymentCommandFake) Complete(_ context.Context, _, intent, lease string) error {
	f.completions++
	if intent != f.intent || lease != f.lease {
		return errors.New("wrong completion proof")
	}
	if f.mode == "ack_failure" && f.completions == 1 {
		return errors.New("lost completion")
	}
	f.done = true
	return nil
}

func TestDunningDurableNonpaymentDeletionSelectionAndFollowup(t *testing.T) {
	for _, mode := range []string{"enabled", "off", "unconfigured", "offline", "withdrawn", "lost_reply", "ack_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			_, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
			acct := seedPastDue(t, ctx, store, "durable-deletion@example.com", api.PlanHobby, now.Add(-22*24*time.Hour))
			app := newApp(t, ctx, store, acct.ID)
			makeLiveInstance(t, ctx, store, app.ID, acct.ID, 256)
			if err := store.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountSuspended); err != nil {
				t.Fatal(err)
			}
			var on atomic.Bool
			on.Store(mode != "off")
			// A saved transition may precede a restart; notifications retain its time.
			fake := &nonpaymentCommandFake{store: store, mode: mode, on: &on, intent: uuid.NewString(), lease: uuid.NewString(), at: now.Add(-time.Hour)}
			var commands meter.NonpaymentDeletionCommands = fake
			if mode == "unconfigured" {
				commands = nil
			}
			d := meter.NewDunning(meter.DunningParams{Store: store, Parker: parker, Notif: notif, Mailer: mailer, Now: func() time.Time { return *now }, Log: discardLog(), NonpaymentDeletionRequested: on.Load, NonpaymentDeletionCommands: commands})
			// Since the billing review of 2026-11-13 (R9-1) nothing is
			// deleted for non-payment, whatever the deletion command's
			// state: a suspended account past 21 days moves to Free.
			if err := d.RunOnce(ctx); err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != state.AccountActive || got.Plan != api.PlanFree {
				t.Fatalf("status %s plan %s, want active on Free", got.Status, got.Plan)
			}
			if fake.calls != 0 || len(notif.byChannel(db.NotifyAccountDeletionPending)) != 0 || len(parker.parked) != 0 {
				t.Fatal("a non-payment deletion was scheduled")
			}
			if mailer.count() != 1 {
				t.Fatalf("mails = %d, want the moved-to-Free mail", mailer.count())
			}
		})
	}
}
