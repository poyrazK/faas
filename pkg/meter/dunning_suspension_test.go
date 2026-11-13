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

// With the durable suspension command configured and on, the ladder still
// never suspends for non-payment: past the grace the account moves to Free
// and the command is not issued (billing review 2026-11-13, R9-1).
func TestDunningNeverIssuesTheSuspensionCommand(t *testing.T) {
	ctx := t.Context()
	_, store, parker, notif, mailer, now := dunningTestFixture(t, ctx)
	acct := seedPastDue(t, ctx, store, "durable-suspension@example.com", api.PlanHobby, now.Add(-8*24*time.Hour))
	app := newApp(t, ctx, store, acct.ID)
	makeLiveInstance(t, ctx, store, app.ID, acct.ID, 256)
	var on atomic.Bool
	on.Store(true)
	fake := &suspensionCommandFake{store: store, mode: "enabled", on: &on, intent: uuid.NewString(), lease: uuid.NewString(), at: *now}
	d := meter.NewDunning(meter.DunningParams{Store: store, Parker: parker, Notif: notif, Mailer: mailer, Now: func() time.Time { return *now }, Log: discardLog(), SuspensionRequested: on.Load, SuspensionCommands: fake})
	if err := d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != state.AccountActive || got.Plan != api.PlanFree || fake.calls != 0 || len(parker.parked) != 0 {
		t.Fatalf("status %s plan %s, command calls %d, parked %d; want active on Free and no suspension", got.Status, got.Plan, fake.calls, len(parker.parked))
	}
	if len(notif.byChannel(db.NotifyBillingPastDue)) != 1 || mailer.count() != 1 {
		t.Fatal("the move to Free was not announced once")
	}
}
