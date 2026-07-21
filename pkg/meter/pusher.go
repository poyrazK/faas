package meter

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/stripex"
	"github.com/onebox-faas/faas/pkg/wire"
)

// StripePusher is the slice of pkg/stripex the hourly pusher uses. The
// interface lives here so meterd can be wired against a no-op in tests
// and the real Client (Slice 2) in production — both directions of the
// dependency are recorded in ADR-019.
type StripePusher interface {
	PushUsageRecord(ctx context.Context, account state.Account, hour time.Time, gbHours float64) error
}

// Pusher is the meterd daemon's hourly Stripe loop. It walks every paid
// account with a stripe_customer_id, sums the past hour's billable
// GB-RAM-hours, and pushes a metered usage record. Stripe's API
// idempotency-key (per (account, hour)) sits on top of the SDK call so a
// retry is safe; meterd's own loop is at-least-once too — a redelivered
// hour is just a duplicate that Stripe collapses.
type Pusher struct {
	store  state.Store
	stripe StripePusher
	log    *slog.Logger
	now    func() time.Time
	ops    *wire.OpsMetrics
}

// NewPusher wires the pusher. now defaults to time.Now if nil so callers
// in production can leave it blank; tests inject a clock. ops defaults
// to a fresh test registry when nil so existing tests don't have to
// construct one — mirrors Loop.NewLoop's nil coercion.
func NewPusher(store state.Store, stripe StripePusher, log *slog.Logger, now func() time.Time, ops *wire.OpsMetrics) *Pusher {
	if now == nil {
		now = time.Now
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("meter_test")
	}
	return &Pusher{store: store, stripe: stripe, log: log, now: now, ops: ops}
}

// HourWindow returns the [start, end) hour boundary the pusher aggregates
// against. end is exclusive so a tick at 14:00:00 covers 13:00–14:00. The
// caller (PushHour) reads usage rows whose minute ∈ [start, end).
func HourWindow(at time.Time) (start, end time.Time) {
	start = at.UTC().Truncate(time.Hour).Add(-time.Hour)
	end = at.UTC().Truncate(time.Hour)
	return start, end
}

// PushHour pushes the billable GB-hours for one hour for every paid
// account. Free accounts are skipped (no Stripe customer, no overage).
// Returns the number of accounts it pushed for so the loop can log a
// line; errors push loop backoff decisions up to the caller.
//
// Each non-skip SDK call is observed under the "stripe" op with a code
// label from stripex.ClassifyPushError — "ok" on success, a stable
// failure-mode label (card-error, rate-limit, invalid-request, etc.)
// on failure. The skip branches (gb <= 0, free plan, suspended,
// missing usage rows) are silent so the dashboard distinguishes
// "did nothing" from "tried and Stripe bounced it".
func (p *Pusher) PushHour(ctx context.Context) (int, error) {
	if p.stripe == nil {
		return 0, errors.New("meter: stripe pusher not configured")
	}
	now := p.now()
	start, end := HourWindow(now)
	accounts, err := p.store.ListAllAccounts(ctx)
	if err != nil {
		return 0, err
	}
	pushed := 0
	for _, acct := range accounts {
		if acct.Plan == "free" {
			// Free plan: no Stripe customer, no overage — skip.
			continue
		}
		if acct.Status == state.AccountSuspended || acct.Status == state.AccountDeletedPending {
			continue
		}
		rows, err := p.store.UsageByHour(ctx, acct.ID, start, end)
		if err != nil {
			p.log.Warn("meter: usage_by_hour", "account", acct.ID, "err", err)
			continue
		}
		var mbSec int64
		for _, u := range rows {
			mbSec += u.MBSeconds
		}
		gb := GBHours(mbSec)
		if gb <= 0 {
			continue
		}
		pushStart := time.Now()
		perr := p.stripe.PushUsageRecord(ctx, acct, start, gb)
		code := stripex.ClassifyPushError(perr)
		dur := time.Since(pushStart)
		p.ops.ObserveCode("stripe", code, dur)
		p.ops.StripePushDuration(code).Observe(dur.Seconds())
		if perr != nil {
			p.log.Warn("meter: push usage", "account", acct.ID, "hour", start,
				"code", code, "err", perr)
			continue
		}
		pushed++
	}
	return pushed, nil
}
