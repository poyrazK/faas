// Package meter — Dunning timer (spec §4.7, §17 dunning state machine).
//
// The dunning state machine payment_failed → past_due → 7d →
// suspended → 21d → deleted_pending is half-wired by the apid webhook
// (invoice.payment_failed stamps past_due_at; payment_succeeded
// flips it back to active) but the time-driven 7-day and 21-day
// transitions have no timer. Dunning is that timer — it lives in
// meterd alongside the sample / quota / stripe loops because:
//
//   - The transitions are part of the billing surface (spec §4.7).
//   - The 7-day tick needs the same schedd.ParkInstance seam the
//     quota loop already uses (Free hard stop), so a single process
//     keeps the dependencies in scope.
//   - meterd owns the only quota-aware ParkInstance writer today;
//     moving dunning elsewhere would split that responsibility.
//
// The shape mirrors pkg/grace (Params / New / Run / RunOnce) but the
// two timers stay separate per ADR-021 "Future work" — they share the
// tiny Sender interface only, never the rest of the type.
package meter

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// DunningSender is the slice of mail.Sender the dunning timer needs
// for its transition emails. Kept as a local interface so pkg/meter
// doesn't pull pkg/mail (which would create a graph cycle through
// pkg/api). The signature matches mail.Sender exactly so cmd/meterd
// can pass its mailer directly without an adapter.
type DunningSender interface {
	Send(ctx context.Context, msg mail.Message) error
}

// DunningParams wires the runtime dependencies the Dunning loop
// needs. Store is required; the rest fall back to no-op defaults
// (mirrors pkg/grace.Params).
type DunningParams struct {
	Store    state.Store
	Parker   ScheddParker
	Mailer   DunningSender
	Notif    Notifier
	Interval time.Duration // default 1h
	Now      func() time.Time
	Log      *slog.Logger
}

// Dunning is the 7-day / 21-day dunning timer. Owns the ticker + the
// sweep loop; Run exits cleanly when ctx is cancelled.
type Dunning struct {
	p DunningParams
}

// NewDunning returns a Dunning ready to Run. Defaults: Interval 1h,
// Now time.Now, Log slog.Default, Mailer/Noop, Notif swallowed.
func NewDunning(p DunningParams) *Dunning {
	if p.Store == nil {
		panic("meter: dunningParams.Store is required")
	}
	if p.Interval <= 0 {
		p.Interval = time.Hour
	}
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	if p.Mailer == nil {
		p.Mailer = noopDunningSender{}
	}
	if p.Notif == nil {
		p.Notif = noopDunningNotifier{}
	}
	return &Dunning{p: p}
}

// noopDunningNotifier swallows every notify call. Default for tests.
type noopDunningNotifier struct{}

func (noopDunningNotifier) Notify(_ context.Context, _, _ string) error { return nil }

// noopDunningSender discards every message. Default for tests.
type noopDunningSender struct{}

func (noopDunningSender) Send(_ context.Context, _ mail.Message) error { return nil }

// dunningPayload is the JSON shape the pg_notify channel subscribers
// (audit, dashboard) parse. Kept minimal — account_id is enough to
// evict everything keyed by the account.
type dunningPayload struct {
	AccountID string `json:"account_id"`
	At        string `json:"at"`
}

// DunningPastDueToSuspendedDuration is the time-from-past_due_at
// before the timer advances an account to suspended. Spec §4.7.
const DunningPastDueToSuspendedDuration = 7 * 24 * time.Hour

// DunningSuspendedToDeletedDuration is the *additional* time
// from past_due_at before the timer advances an account to
// deleted_pending. Total = 21 days from past_due_at, the spec's
// grace window. Stored as a delta so a back-and-forth status flip
// doesn't accumulate drift — both transitions compare against
// PastDueAt, never stacked elapsed.
const DunningSuspendedToDeletedDuration = 21 * 24 * time.Hour

// Run loops on Interval until ctx is cancelled. Each tick calls
// RunOnce; per-tick errors are logged and swallowed so a transient
// store outage doesn't kill the loop (matches pkg/grace.Run).
func (d *Dunning) Run(ctx context.Context) error {
	t := time.NewTicker(d.p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := d.RunOnce(ctx); err != nil {
				d.p.Log.Warn("meter: dunning tick failed", "err", err)
			}
		}
	}
}

// RunOnce walks every account and applies the per-status ladder:
//   - past_due   past past_due_at + 7d   → suspended + park every
//     live instance + email + notify billing_past_due.
//   - suspended  past past_due_at + 21d  → deleted_pending + notify
//     account_deletion_pending + email. (No additional ParkInstance
//     — instances were already parked on the suspended transition.)
//   - past_due with PastDueAt == nil   → backfill stamp via
//     MarkDunningStep(past_due, past_due) (no-op status flip that
//     triggers the SQL's coalesce) + warn log + skip transition.
//
// Exported so tests drive the sweep deterministically without
// spinning up a real ticker.
func (d *Dunning) RunOnce(ctx context.Context) error {
	rows, err := d.p.Store.ListAllAccounts(ctx)
	if err != nil {
		return err
	}
	now := d.p.Now()
	for _, acct := range rows {
		switch acct.Status {
		case state.AccountPastDue:
			if acct.PastDueAt == nil {
				// Data-integrity guard: account is past_due but the
				// migration column never got stamped (likely an account
				// that entered past_due before the migration landed).
				// Backfill via MarkDunningStep(past_due, past_due) —
				// the no-op status flip triggers the SQL's
				// `coalesce(past_due_at, $3)` to write the stamp.
				// Skip the transition this tick; the next tick will
				// pick up the now-stamped row.
				if err := d.p.Store.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountPastDue); err != nil {
					d.p.Log.Warn("meter: dunning backfill stamp", "account", acct.ID, "err", err)
					continue
				}
				d.p.Log.Warn("meter: dunning backfilled past_due_at on legacy row",
					"account", acct.ID)
				continue
			}
			if now.Sub(*acct.PastDueAt) < DunningPastDueToSuspendedDuration {
				continue // still inside the 7-day grace
			}
			if err := d.p.Store.MarkDunningStep(ctx, acct.ID, state.AccountPastDue, state.AccountSuspended); err != nil {
				if errors.Is(err, state.ErrNotFound) {
					continue // redelivery race or status flipped under us
				}
				d.p.Log.Warn("meter: dunning suspend", "account", acct.ID, "err", err)
				continue
			}
			d.parkAll(ctx, acct.ID)
			payload, _ := json.Marshal(dunningPayload{AccountID: acct.ID, At: now.UTC().Format(time.RFC3339Nano)})
			if err := d.p.Notif.Notify(ctx, db.NotifyBillingPastDue, string(payload)); err != nil {
				d.p.Log.Warn("meter: dunning notify billing_past_due", "account", acct.ID, "err", err)
			}
			d.sendSuspendedMail(ctx, acct, now)
			d.p.Log.Info("meter: dunning past_due→suspended", "account", acct.ID)
		case state.AccountSuspended:
			if acct.PastDueAt == nil {
				continue
			}
			if now.Sub(*acct.PastDueAt) < DunningSuspendedToDeletedDuration {
				continue // still inside the 21-day window
			}
			if err := d.p.Store.MarkDunningStep(ctx, acct.ID, state.AccountSuspended, state.AccountDeletedPending); err != nil {
				if errors.Is(err, state.ErrNotFound) {
					continue
				}
				d.p.Log.Warn("meter: dunning delete_pending", "account", acct.ID, "err", err)
				continue
			}
			payload, _ := json.Marshal(dunningPayload{AccountID: acct.ID, At: now.UTC().Format(time.RFC3339Nano)})
			if err := d.p.Notif.Notify(ctx, db.NotifyAccountDeletionPending, string(payload)); err != nil {
				d.p.Log.Warn("meter: dunning notify account_deletion_pending", "account", acct.ID, "err", err)
			}
			d.sendDeletionMail(ctx, acct, now)
			d.p.Log.Info("meter: dunning suspended→deleted_pending", "account", acct.ID)
		}
	}
	return nil
}

// parkAll is the per-account ParkInstance fanout. Mirrors the shape of
// pkg/meter/quota.go::EnforceQuota's "stop" branch (Free hard stop):
// one call per live instance, per-instance failures logged + skipped
// (a partial park doesn't poison the rest of the sweep).
func (d *Dunning) parkAll(ctx context.Context, accountID string) {
	ins, err := d.p.Store.ListInstancesForAccount(ctx, accountID)
	if err != nil {
		d.p.Log.Warn("meter: dunning list instances", "account", accountID, "err", err)
		return
	}
	for _, in := range ins {
		if !state.State(in.State).CountsForRAM() {
			continue
		}
		if err := d.p.Parker.ParkInstance(ctx, in.ID, "dunning_past_due_7d"); err != nil {
			d.p.Log.Warn("meter: dunning park instance", "instance", in.ID, "err", err)
			continue
		}
	}
}

// sendSuspendedMail delivers the "your apps are now suspended" email.
// The body is defined in pkg/mail.AccountSuspendedBody — kept there
// so all account-lifecycle email content stays in one reviewable
// file. Mirrors pkg/grace's relationship to pkg/mail.
func (d *Dunning) sendSuspendedMail(ctx context.Context, acct state.Account, at time.Time) {
	subject, body := mail.AccountSuspendedBody(acct.Email, at)
	msg := mail.Message{To: []string{acct.Email}, Subject: subject, TextBody: body}
	if err := d.p.Mailer.Send(ctx, msg); err != nil {
		d.p.Log.Warn("meter: dunning suspended mail", "account", acct.ID, "err", err)
	}
}

// sendDeletionMail delivers the "your account is scheduled for hard
// delete" email. Reuses the AccountDeletionPendingBody template — the
// same shape as the customer-initiated flow because the customer sees
// the same outcome (a hard delete in 30 days from the suspended step).
func (d *Dunning) sendDeletionMail(ctx context.Context, acct state.Account, at time.Time) {
	pastDueAt := at // default for accounts that lost the original stamp
	if acct.PastDueAt != nil {
		pastDueAt = *acct.PastDueAt
	}
	subject, body := mail.AccountDeletionPendingBody(acct.Email, pastDueAt, at.Add(30*24*time.Hour))
	msg := mail.Message{To: []string{acct.Email}, Subject: subject, TextBody: body}
	if err := d.p.Mailer.Send(ctx, msg); err != nil {
		d.p.Log.Warn("meter: dunning deletion mail", "account", acct.ID, "err", err)
	}
}