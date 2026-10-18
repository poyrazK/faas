// adr: 20261017133739
// Durable quota warnings retain saved observations and independent follow-up gates.
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

type quotaCommandFake struct {
	store              *state.MemStore
	mode               string
	on                 *atomic.Bool
	result             api.InternalMailQuotaWarningResponse
	calls, completions int
	done               bool
}

func (f *quotaCommandFake) Warn(ctx context.Context, id string, day time.Time, plan api.Plan) (api.InternalMailQuotaWarningResponse, error) {
	f.calls++
	if f.mode == "offline" {
		return api.InternalMailQuotaWarningResponse{}, errors.New("offline")
	}
	if !day.Equal(f.result.Day) || plan != f.result.Plan {
		return api.InternalMailQuotaWarningResponse{}, errors.New("wrong expected day/plan")
	}
	if _, err := f.store.LoadAndStampLastQuotaWarning(ctx, id, day); err != nil {
		return api.InternalMailQuotaWarningResponse{}, err
	}
	if f.mode == "lost_reply" && f.calls == 1 {
		return api.InternalMailQuotaWarningResponse{}, errors.New("lost response after commit")
	}
	if f.mode == "withdrawn" {
		f.on.Store(false)
	}
	result := f.result
	result.Followup = !f.done
	return result, nil
}

func (f *quotaCommandFake) Complete(_ context.Context, _, intent, lease string) error {
	f.completions++
	if intent != f.result.IntentID || lease != f.result.LeaseID {
		return errors.New("wrong completion proof")
	}
	if f.mode == "ack_failure" && f.completions == 1 {
		return errors.New("lost completion")
	}
	f.done = true
	return nil
}

func TestQuotaWarningDurableSelectionAndSavedFollowup(t *testing.T) {
	for _, mode := range []string{"enabled", "off", "unconfigured", "offline", "withdrawn", "lost_reply", "ack_failure"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			store := state.NewMemStore()
			acct := makeAccount(t, ctx, store, api.PlanPro)
			notif, parker, mailer := &fakeNotifier{}, &fakeParker{}, &fakeMailer{}
			now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
			var on atomic.Bool
			on.Store(mode != "off")
			fake := &quotaCommandFake{store: store, mode: mode, on: &on, result: api.InternalMailQuotaWarningResponse{
				IntentID: uuid.NewString(), LeaseID: uuid.NewString(), Day: now.Truncate(24 * time.Hour), ObservedAt: now.Add(-time.Hour), Plan: api.PlanPro, UsedMBSeconds: int64(api.PlanPro.PlanIncludedGBHours()+1) * 1024 * 3600, QuotaGB: api.PlanPro.PlanIncludedGBHours(),
			}}
			var commands meter.QuotaWarningCommands = fake
			if mode == "unconfigured" {
				commands = nil
			}
			p := meter.QuotaParams{Store: store, Notif: notif, Parker: parker, Mailer: mailer, Log: discardLog(), WarningRequested: on.Load, WarningCommands: commands}
			run := func() {
				t.Helper()
				// Current tick values differ from the persisted warning snapshot.
				if _, err := p.Enforce(ctx, acct, float64(api.PlanPro.PlanIncludedGBHours()+50), now); err != nil {
					t.Fatal(err)
				}
			}
			run()
			got, err := store.AccountByID(ctx, acct.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unconfigured" || mode == "offline" {
				if got.LastQuotaWarningAt != nil || mailer.count() != 0 || len(notif.byChannel(db.NotifyQuotaWarning)) != 0 {
					t.Fatal("selected failure consumed legacy daily stamp or sent inline")
				}
				return
			}
			if got.LastQuotaWarningAt == nil {
				t.Fatal("daily stamp missing")
			}
			if mode == "lost_reply" || mode == "withdrawn" {
				if len(notif.byChannel(db.NotifyQuotaWarning)) != 0 || mailer.count() != 0 {
					t.Fatal("uncertain/withdrawn result ran follow-up")
				}
				if mode == "withdrawn" {
					return
				}
				run()
			}
			if mode == "ack_failure" {
				run()
			}
			want := 1
			if mode == "ack_failure" {
				want = 2
			}
			notices := notif.byChannel(db.NotifyQuotaWarning)
			if len(notices) != want || len(parker.parked) != 0 {
				t.Fatal("wrong follow-up or unexpected scheduler calls", len(notices), want)
			}
			if mode == "off" {
				if fake.calls != 0 || mailer.count() != 1 {
					t.Fatal("flag-off legacy path changed")
				}
				return
			}
			var payload struct {
				At      string  `json:"at"`
				UsedGB  float64 `json:"used_gb"`
				QuotaGB int     `json:"quota_gb"`
				Plan    string  `json:"plan"`
			}
			if err := json.Unmarshal([]byte(notices[0].Payload), &payload); err != nil || payload.At != fake.result.ObservedAt.UTC().Format(time.RFC3339Nano) || payload.UsedGB != float64(api.PlanPro.PlanIncludedGBHours()+1) || payload.QuotaGB != api.PlanPro.PlanIncludedGBHours() || payload.Plan != "pro" {
				t.Fatal("follow-up replaced saved snapshot with current tick", payload, err)
			}
			if mailer.count() != 0 || !fake.done {
				t.Fatal("durable path sent inline or failed completion")
			}
			run()
			if len(notif.byChannel(db.NotifyQuotaWarning)) != want || mailer.count() != 0 {
				t.Fatal("completed follow-up repeated")
			}
		})
	}
}

func TestQuotaWarningSelectedFailureStillLiftsFreeStopAfterUpgrade(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	acct := makeAccount(t, ctx, store, api.PlanFree)
	if _, err := store.SuspendAccountForFreeQuota(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountPlan(ctx, acct.ID, api.PlanPro); err != nil {
		t.Fatal(err)
	}
	acct, err := store.AccountByID(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := meter.QuotaParams{Store: store, Notif: &fakeNotifier{}, Parker: &fakeParker{}, Mailer: &fakeMailer{}, Log: discardLog(), WarningRequested: func() bool { return true }}
	if _, err := p.Enforce(ctx, acct, 300, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, err := store.AccountByID(ctx, acct.ID)
	if err != nil || got.Status != state.AccountActive {
		t.Fatal("mail failure blocked quota restoration", got.Status, err)
	}
}
