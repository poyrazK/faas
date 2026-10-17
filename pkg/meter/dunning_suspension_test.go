// adr: 20261017115753
// Suspension mail commands retain paired transitions and exact opt-in behavior.
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

type suspensionCommandFake struct {
	store              *state.MemStore
	mode               string
	on                 *atomic.Bool
	intent, lease      string
	at                 time.Time
	calls, completions int
	done               bool
}

func (f *suspensionCommandFake) Suspend(ctx context.Context, id string, _ time.Time) (api.InternalMailSuspensionResponse, error) {
	f.calls++
	if f.mode == "offline" {
		return api.InternalMailSuspensionResponse{}, errors.New("offline")
	}
	a, err := f.store.AccountByID(ctx, id)
	if err != nil {
		return api.InternalMailSuspensionResponse{}, err
	}
	if a.Status == state.AccountPastDue {
		if err := f.store.MarkDunningStep(ctx, id, state.AccountPastDue, state.AccountSuspended); err != nil {
			return api.InternalMailSuspensionResponse{}, err
		}
	}
	if f.mode == "lost_reply" && f.calls == 1 {
		return api.InternalMailSuspensionResponse{}, errors.New("lost response after commit")
	}
	if f.mode == "withdrawn" {
		f.on.Store(false)
	}
	return api.InternalMailSuspensionResponse{IntentID: f.intent, LeaseID: f.lease, SuspendedAt: f.at, Followup: !f.done}, nil
}

func (f *suspensionCommandFake) Complete(context.Context, string, string, string) error {
	f.completions++
	if f.mode == "ack_failure" && f.completions == 1 {
		return errors.New("lost completion")
	}
	f.done = true
	return nil
}

func TestDunningDurableSuspensionSelectionAndFollowup(t *testing.T) {
	for _, mode := range []string{"enabled", "off", "unconfigured", "offline", "withdrawn", "lost_reply", "ack_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			_, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
			acct := seedPastDue(t, ctx, store, "durable-suspension@example.com", api.PlanHobby, now.Add(-8*24*time.Hour))
			app := newApp(t, ctx, store, acct.ID)
			ins := makeLiveInstance(t, ctx, store, app.ID, acct.ID, 256)
			var on atomic.Bool
			on.Store(mode != "off")
			fake := &suspensionCommandFake{store: store, mode: mode, on: &on, intent: uuid.NewString(), lease: uuid.NewString(), at: *now}
			var commands meter.SuspensionCommands = fake
			if mode == "unconfigured" {
				commands = nil
			}
			d := meter.NewDunning(meter.DunningParams{Store: store, Parker: parker, Notif: notif, Mailer: mailer, Now: func() time.Time { return *now }, Log: discardLog(), SuspensionRequested: on.Load, SuspensionCommands: commands})
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unconfigured" || mode == "offline" {
				if got.Status != state.AccountPastDue || len(parker.parked) != 0 || mailer.count() != 0 {
					t.Fatal("selected failure fell back to legacy transition")
				}
				return
			}
			if got.Status != state.AccountSuspended {
				t.Fatal("suspension did not commit", got.Status)
			}
			if mode == "lost_reply" || mode == "withdrawn" {
				if len(parker.parked) != 0 || mailer.count() != 0 {
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
			if len(parker.parked) != want || parker.parked[0].InstanceID != ins.ID || parker.parked[0].Reason != "dunning_past_due_7d" || len(notif.byChannel(db.NotifyBillingPastDue)) != want {
				t.Fatal("existing parking/notification follow-up changed")
			}
			if mode == "off" {
				if fake.calls != 0 || mailer.count() != 1 {
					t.Fatal("flag-off legacy path changed")
				}
				return
			}
			if mailer.count() != 0 || !fake.done {
				t.Fatal("durable path sent inline or failed to acknowledge follow-up")
			}
			if err := d.RunOnce(ctx); err != nil {
				t.Fatal(err)
			}
			if len(parker.parked) != want || mailer.count() != 0 {
				t.Fatal("completed follow-up repeated")
			}
		})
	}
}
