package meter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/paddle"
	"github.com/onebox-faas/faas/pkg/billing/polar"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Pusher is the meterd daemon's billing-pusher loop. It walks every paid
// account with a customer id (Stripe cus_… or Paddle ctm_…), sums the past
// billing window's mb_seconds, and pushes a single metered usage
// record. The active provider (Polar by default, or the explicit
// FAAS_BILLING_PROVIDER selection) is dispatched through billing.Provider — the local
// StripePusher interface that predated PR #3 was collapsed into the Provider
// interface so the same pusher loop runs on either provider.
//
// Stripe's API idempotency-key (per (account, hour)) sits on top of the SDK
// call so a retry is safe; meterd's own loop is at-least-once too — a
// redelivered window is just a duplicate that the provider collapses.
//
// The production cadence is hourly. Each pass reads a bounded history of
// completed UTC-hour windows from Postgres and replays every positive window
// whose provider dedupe key has not completed. This makes a restart, missed
// ticker, or temporary provider outage recoverable without double billing.
type Pusher struct {
	store  state.Store
	pusher billing.Provider
	log    *slog.Logger
	now    func() time.Time
	ops    *wire.OpsMetrics
}

// NewPusher wires the pusher. now defaults to time.Now if nil so callers
// in production can leave it blank; tests inject a clock. ops defaults
// to a fresh test registry when nil so existing tests don't have to
// construct one — mirrors Loop.NewLoop's nil coercion.
func NewPusher(store state.Store, pusher billing.Provider, log *slog.Logger, now func() time.Time, ops *wire.OpsMetrics) *Pusher {
	if now == nil {
		now = time.Now
	}
	if ops == nil {
		ops = wire.NewOpsMetrics("meter_test")
	}
	return &Pusher{store: store, pusher: pusher, log: log, now: now, ops: ops}
}

// Provider returns the underlying billing.Provider so sibling
// goroutines (the drift-detector reconciler, ADR-049 §B.1) can
// share the same Stripe/Paddle handle without meterd loading a
// second client. cmd/meterd/main.go uses this accessor to wire
// reconciler.New(...) once at boot.
func (p *Pusher) Provider() billing.Provider {
	return p.pusher
}

// HourWindow returns the [start, end) hour boundary the pusher aggregates
// against. end is exclusive so a tick at 14:00:00 covers 13:00–14:00. The
// caller (PushHour) reads usage rows whose minute ∈ [start, end).
func HourWindow(at time.Time) (start, end time.Time) {
	start = at.UTC().Truncate(time.Hour).Add(-time.Hour)
	end = at.UTC().Truncate(time.Hour)
	return start, end
}

// PushHour pushes the provider's billable quantity for one billing window for
// every paid account. Free accounts are skipped (no customer id, no
// overage). Returns the number of accounts it pushed for so the loop
// can log a line; errors push loop backoff decisions up to the caller.
//
// PushHour remains the single-window compatibility/test seam. Production
// uses PushPending below so a missed hourly tick is recovered from the
// durable lookback. The integer mb_seconds sum is handed to the SDK, which
// converts to wire units in pure int64 arithmetic.
//
// Each non-skip SDK call is observed under the "stripe" op with a code
// label from stripe.ClassifyPushError — "ok" on success, a stable
// failure-mode label (card-error, rate-limit, invalid-request, etc.)
// on failure. The skip branches (mbSec <= 0, free plan, suspended,
// missing usage rows) are silent so the dashboard distinguishes
// "did nothing" from "tried and the provider bounced it".
//
// Paddle's CreateUsageRecord is the parallel surface — PR #3 wires the
// loop dispatch through billing.Provider so the same body drives
// either SDK. The per-provider classify/observe pair
// (pusherDispatch, classifyProviderError — below) is the dispatch
// seam: each provider gets a stable label set and a parallel
// `_paddle_push_duration_seconds` histogram so the dashboard panels
// for Paddle and Stripe render with the same grain. The classifiers
// (stripe.ClassifyPushError, paddle.ClassifyPushError) are
// SDK-typed and live in each provider's package so the pusher
// doesn't depend on either SDK's internal error shape.
func (p *Pusher) PushHour(ctx context.Context) (int, error) {
	if p.pusher == nil {
		return 0, errors.New("meter: billing pusher not configured")
	}
	now := p.now()
	start, end := HourWindow(now)
	accounts, err := p.store.ListAllAccounts(ctx)
	if err != nil {
		return 0, err
	}
	// Snapshot the provider dispatch once so the per-account loop
	// doesn't re-type-switch on every iteration. ops.opLabel is
	// stamped on ops_total and the per-provider PushDuration
	// histogram; ops.classify maps a push error to the closed
	// per-provider label set; ops.observe records the duration.
	// All three live on the same struct (providerOpsFor) so the
	// per-provider seam is one type-switch instead of two — the
	// PR #204-era pusherDispatch + classifyProviderError pair had
	// drifted into two parallel seams over the same provider
	// surface. See providerOpsFor below.
	ops := providerOpsFor(p.pusher)
	pushed := 0
	for _, acct := range accounts {
		if acct.Plan == "free" {
			// Free plan: no customer id, no overage — skip.
			continue
		}
		if acct.Status == state.AccountSuspended || acct.Status == state.AccountDeletedPending {
			continue
		}
		if acct.ProviderCustomerID == "" || acct.StripeSubscriptionItem == "" {
			// Paid usage is not billable until both provider identities have
			// been persisted by the subscription webhook. Calling a provider
			// with an incomplete account can be a successful no-op (Polar and
			// Stripe both use that shape), which would falsely advance the
			// pusher's success count and leave usage unbilled.
			p.log.Warn("meter: billing identity incomplete",
				"account", acct.ID, "provider_customer_id_set", acct.ProviderCustomerID != "",
				"subscription_id_set", acct.StripeSubscriptionItem != "")
			continue
		}
		// The provider impl's PushUsageRecord is the source-of-truth skip
		// when an account has no customer id (stripe.Client::PushUsageRecord
		// returns nil silently on empty cus_… — see
		// pkg/billing/stripe/client.go:117-125). We forward every
		// non-Free, non-Suspended/DeletedPending account so the SDK
		// gets a chance to log the skip + the dedupe record stays
		// consistent for both providers.
		rows, err := p.store.UsageByHour(ctx, acct.ID, start, end)
		if err != nil {
			p.log.Warn("meter: usage_by_hour", "account", acct.ID, "err", err)
			continue
		}
		var mbSec int64
		for _, u := range rows {
			mbSec += u.MBSeconds
		}
		if mbSec <= 0 {
			continue
		}
		billableMBSeconds, err := p.billableUsage(ctx, acct, start, mbSec)
		if err != nil {
			p.log.Warn("meter: calculate billable usage", "account", acct.ID, "hour", start, "mb_seconds", mbSec, "err", err)
			continue
		}
		if billableMBSeconds <= 0 {
			continue
		}
		pushStart := time.Now()
		perr := p.pusher.PushUsageRecord(ctx, acct, start, billableMBSeconds)
		code := ops.classify(perr)
		dur := time.Since(pushStart)
		p.ops.ObserveCode(ops.opLabel, code, dur)
		ops.observe(p.ops, code, dur)
		if perr != nil {
			p.log.Warn("meter: push usage", "account", acct.ID, "hour", start,
				"code", code, "mb_seconds", mbSec, "billable_mb_seconds", billableMBSeconds, "err", perr)
			continue
		}
		p.log.Info("meter: push usage", "account", acct.ID, "hour", start,
			"code", code, "mb_seconds", mbSec, "billable_mb_seconds", billableMBSeconds)
		pushed++
	}
	return pushed, nil
}

// PushPending replays all positive completed usage windows in lookback. The
// source rows are durable usage_minutes aggregates; provider implementations
// claim/record their own (account, hour) idempotency key, so an hourly pass is
// an at-least-once delivery mechanism rather than a best-effort "latest hour"
// cursor. Per-window failures do not stop the rest of the sweep, but the first
// error is returned so the meterd health surface and next tick can see it.
func (p *Pusher) PushPending(ctx context.Context, lookback time.Duration) (int, error) {
	if p.pusher == nil {
		return 0, errors.New("meter: billing pusher not configured")
	}
	if lookback <= 0 {
		lookback = 30 * 24 * time.Hour
	}
	end := p.now().UTC().Truncate(time.Hour)
	start := end.Add(-lookback).Truncate(time.Hour)
	windows, err := p.store.UsageWindows(ctx, start, end)
	if err != nil {
		return 0, err
	}
	accounts, err := p.store.ListAllAccounts(ctx)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]state.Account, len(accounts))
	for _, acct := range accounts {
		byID[acct.ID] = acct
	}
	ops := providerOpsFor(p.pusher)
	mode := providerUsageMode(p.pusher)
	var caps map[string]int64
	if mode == billing.UsageModeOverage {
		// A cap is a billing safety boundary, so a read failure must fail
		// closed. The bulk read keeps replay at one database round-trip
		// instead of turning every usage window into an account lookup.
		caps, err = p.store.LoadAllOverageCapCents(ctx)
		if err != nil {
			return 0, fmt.Errorf("meter: load overage caps: %w", err)
		}
	}
	ordered := append([]state.UsageWindow(nil), windows...)
	if mode == billing.UsageModeOverage {
		// Postgres already returns hour order; sorting the copy also makes the
		// in-memory implementation and narrow test stores obey the same
		// cumulative-month contract.
		sort.SliceStable(ordered, func(i, j int) bool {
			left, right := ordered[i], ordered[j]
			if !left.Hour.Equal(right.Hour) {
				return left.Hour.Before(right.Hour)
			}
			return left.AccountID < right.AccountID
		})
	}
	cursors := make(map[string]overageCursor)
	pushed := 0
	var firstErr error
	for _, window := range ordered {
		acct, ok := byID[window.AccountID]
		if !ok || acct.Plan == "free" || acct.Status == state.AccountSuspended || acct.Status == state.AccountDeletedPending {
			continue
		}
		if acct.ProviderCustomerID == "" || acct.StripeSubscriptionItem == "" {
			// Keep the window in durable usage_minutes until the subscription
			// webhook stamps both identities. This avoids reporting a successful
			// provider push for a provider no-op and lets the normal lookback
			// replay it after checkout completes.
			p.log.Warn("meter: billing identity incomplete",
				"account", acct.ID, "provider_customer_id_set", acct.ProviderCustomerID != "",
				"subscription_id_set", acct.StripeSubscriptionItem != "", "replay", true)
			continue
		}
		billableMBSeconds := window.MBSeconds
		if mode == billing.UsageModeOverage {
			billableMBSeconds, err = p.billablePendingUsage(ctx, acct, window, cursors, caps)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				p.log.Warn("meter: calculate replay billable usage", "account", acct.ID, "hour", window.Hour, "mb_seconds", window.MBSeconds, "err", err)
				continue
			}
		}
		if billableMBSeconds <= 0 {
			continue
		}
		pushStart := time.Now()
		perr := p.pusher.PushUsageRecord(ctx, acct, window.Hour, billableMBSeconds)
		code := ops.classify(perr)
		dur := time.Since(pushStart)
		p.ops.ObserveCode(ops.opLabel, code, dur)
		ops.observe(p.ops, code, dur)
		if perr != nil {
			if firstErr == nil {
				firstErr = perr
			}
			p.log.Warn("meter: push usage", "account", acct.ID, "hour", window.Hour,
				"replay", true, "code", code, "mb_seconds", window.MBSeconds, "billable_mb_seconds", billableMBSeconds, "err", perr)
			continue
		}
		pushed++
		p.log.Info("meter: push usage", "account", acct.ID, "hour", window.Hour,
			"replay", true, "code", code, "mb_seconds", window.MBSeconds, "billable_mb_seconds", billableMBSeconds)
	}
	return pushed, firstErr
}

type overageCursor struct {
	rawBefore int64
}

func providerUsageMode(provider billing.Provider) billing.UsageMode {
	if modeProvider, ok := provider.(billing.UsageModeProvider); ok {
		if mode := modeProvider.UsageMode(); mode == billing.UsageModeOverage {
			return mode
		}
	}
	return billing.UsageModeRaw
}

func (p *Pusher) billableUsage(ctx context.Context, acct state.Account, hour time.Time, raw int64) (int64, error) {
	if providerUsageMode(p.pusher) != billing.UsageModeOverage {
		return raw, nil
	}
	window := hour.UTC().Truncate(time.Hour)
	monthStart := time.Date(window.Year(), window.Month(), 1, 0, 0, 0, 0, time.UTC)
	prior, err := sumUsageRows(ctx, p.store, acct.ID, monthStart, window)
	if err != nil {
		return 0, err
	}
	before := api.BillableMBSeconds(acct.Plan, prior)
	after := api.BillableMBSeconds(acct.Plan, prior+raw)
	capCents, capped, err := p.store.GetAccountOverageCapCents(ctx, acct.ID)
	if err != nil {
		return 0, fmt.Errorf("load overage cap: %w", err)
	}
	if capped && overageCapReached(capCents, before, after) {
		return 0, nil
	}
	return after - before, nil
}

func (p *Pusher) billablePendingUsage(ctx context.Context, acct state.Account, window state.UsageWindow, cursors map[string]overageCursor, caps map[string]int64) (int64, error) {
	hour := window.Hour.UTC().Truncate(time.Hour)
	monthStart := time.Date(hour.Year(), hour.Month(), 1, 0, 0, 0, 0, time.UTC)
	key := acct.ID + "\x00" + monthStart.Format("2006-01")
	cursor, ok := cursors[key]
	if !ok {
		prior, err := sumUsageRows(ctx, p.store, acct.ID, monthStart, hour)
		if err != nil {
			return 0, err
		}
		cursor = overageCursor{rawBefore: prior}
	}
	before := api.BillableMBSeconds(acct.Plan, cursor.rawBefore)
	cursor.rawBefore += window.MBSeconds
	after := api.BillableMBSeconds(acct.Plan, cursor.rawBefore)
	cursors[key] = cursor
	if capCents, capped := caps[acct.ID]; capped && overageCapReached(capCents, before, after) {
		// Do not send a partial window. A partial push would be recorded as
		// complete by the provider's hourly dedupe key and the remainder
		// could never be replayed if the customer later raises the cap.
		return 0, nil
	}
	return after - before, nil
}

// overageCapReached reports whether posting the entire candidate window would
// cross the configured monthly cap. The public financial model prices one
// cent per GB-hour, so cap cents map exactly to cap*SecondsPerGBHour
// billable MB-seconds. Keeping the comparison in MB-seconds avoids rounding
// a candidate down to an arbitrary provider quantity and guarantees that a
// Polar event can never be emitted above the configured ceiling.
func overageCapReached(capCents, billableBefore, billableAfter int64) bool {
	if capCents < 0 {
		return true
	}
	if capCents > math.MaxInt64/api.SecondsPerGBHour {
		return false
	}
	limit := capCents * api.SecondsPerGBHour
	return billableBefore >= limit || billableAfter > limit
}

func sumUsageRows(ctx context.Context, store state.Store, accountID string, start, end time.Time) (int64, error) {
	if !start.Before(end) {
		return 0, nil
	}
	rows, err := store.UsageByHour(ctx, accountID, start, end)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, row := range rows {
		total += row.MBSeconds
	}
	return total, nil
}

// providerOps is the per-provider seam — one struct, three closures,
// one type-switch. Replaces the PR #204-era pair
// (pusherDispatch + classifyProviderError) which had drifted into
// two parallel seams over the same provider surface. A new
// provider PR adds one switch arm here + a histogram in
// metrics.go; no other touch points.
type providerOps struct {
	opLabel  string
	classify func(err error) string
	observe  func(m *wire.OpsMetrics, code string, d time.Duration)
}

// providerOpsFor returns the per-provider observation + classification
// seam for a billing.Provider. The dispatcher is intentionally
// SDK-typed locally — the Provider interface is SDK-agnostic by
// design (ADR-025), but the dispatch itself needs to know which
// SDK to call. Two SDK shapes today (Stripe, Paddle); a new
// provider would extend the switch.
//
// Classification goes through the billing.Classifier optional
// interface (declared at pkg/billing/provider.go) so SDK-typed
// classification stays in the provider's own package (which knows
// about *stripe.Error / *paddleerr.Error) without forcing
// billing.Provider wider. A provider that doesn't implement
// Classifier gets the default "other" label; nil always returns
// "ok" (the closed label set's success label is identical for both
// providers — the dashboard panel joins on `code="ok"`).
//
// The observe closure captures the SDK-typed histogram selector
// method; the alternative — returning a method value — would
// force the pusher to know the method name ahead of dispatch.
func providerOpsFor(prov billing.Provider) providerOps {
	classify := func(err error) string {
		if err == nil {
			return "ok"
		}
		if c, ok := prov.(billing.Classifier); ok {
			return c.ClassifyPushError(err)
		}
		return "other"
	}
	stripeObserve := func(m *wire.OpsMetrics, code string, d time.Duration) {
		m.StripePushDuration(code).Observe(d.Seconds())
	}
	switch prov.(type) {
	case *stripe.Client:
		return providerOps{opLabel: "stripe", classify: classify, observe: stripeObserve}
	case *paddle.Provider:
		return providerOps{opLabel: "paddle", classify: classify, observe: func(m *wire.OpsMetrics, code string, d time.Duration) {
			m.PaddlePushDuration(code).Observe(d.Seconds())
		}}
	case *polar.Provider:
		return providerOps{opLabel: "polar", classify: classify, observe: func(m *wire.OpsMetrics, code string, d time.Duration) {
			m.PolarPushDuration(code).Observe(d.Seconds())
		}}
	default:
		// Unknown provider — fall back to the Stripe observer so
		// observations are not silently dropped. The op label stays
		// "stripe" because the histogram name encodes it; renaming it
		// here would require a new histogram in metrics.go.
		return providerOps{opLabel: "stripe", classify: classify, observe: stripeObserve}
	}
}
