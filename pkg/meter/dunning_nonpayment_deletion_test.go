// adr: 20261017125602
// Non-payment deletion commands retain durable notices and follow-up receipts.
package meter_test

import (
	"context"
	"encoding/json"
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
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(parker.parked) != 0 {
				t.Fatal("deletion step unexpectedly called scheduler")
			}
			if mode == "unconfigured" || mode == "offline" {
				if got.Status != state.AccountSuspended || mailer.count() != 0 || len(notif.byChannel(db.NotifyAccountDeletionPending)) != 0 {
					t.Fatal("selected failure fell back to legacy transition")
				}
				return
			}
			if got.Status != state.AccountDeletedPending {
				t.Fatal("deletion did not commit", got.Status)
			}
			if mode == "lost_reply" || mode == "withdrawn" {
				if len(notif.byChannel(db.NotifyAccountDeletionPending)) != 0 || mailer.count() != 0 {
					t.Fatal("uncertain or withdrawn response ran follow-up")
				}
				if mode == "withdrawn" {
					return
				}
				if err := d.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "ack_failure" {
				if err := d.RunOnce(ctx); err != nil {
					t.Fatal(err)
				}
			}
			want := 1
			if mode == "ack_failure" {
				want = 2
			}
			notices := notif.byChannel(db.NotifyAccountDeletionPending)
			if len(notices) != want {
				t.Fatal("lost deletion notification follow-up", len(notices), want)
			}
			if mode == "off" {
				if fake.calls != 0 || mailer.count() != 1 {
					t.Fatal("flag-off legacy path changed")
				}
				return
			}
			var payload struct {
				At string `json:"at"`
			}
			if err := json.Unmarshal([]byte(notices[0].Payload), &payload); err != nil || payload.At != fake.at.UTC().Format(time.RFC3339Nano) {
				t.Fatal("follow-up rewrote saved transition time", err)
			}
			if mailer.count() != 0 || !fake.done {
				t.Fatal("durable path sent inline or failed to acknowledge follow-up")
			}
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if len(notif.byChannel(db.NotifyAccountDeletionPending)) != want || mailer.count() != 0 {
				t.Fatal("completed follow-up repeated")
			}
		})
	}
}
