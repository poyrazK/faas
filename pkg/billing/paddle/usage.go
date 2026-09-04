package paddle

import (
	"context"
	"errors"
	"fmt"
	"time"

	paddle "github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Sentinel errors for the pre-SDK guard failures. The classifier in
// errors.go uses errors.Is to map these to stable Prometheus labels
// instead of string-fragment matching — adding a sentinel is the
// supported way to introduce a new pre-SDK failure mode.
//
// Mirrors pkg/billing/stripe/usage.go:17-30 (the Stripe pair):
//
//   - ErrNoAPIKey / stripe.ErrNoAPIKey
//   - ErrNegativeMBSeconds / stripe.ErrNegativeQuantity
//     (Paddle wire quantity is 1; the guard is on the int64
//     mb_seconds input, not on wire quantity, so the name differs)
//   - ErrOveragePriceMissing — Paddle-specific; no Stripe analog
//     because Stripe's metered subscription_item is provisioned
//     once and reused, while Paddle's overage price handle is
//     looked up per push from the boot-time catalog. If
//     EnsurePlanProducts has not populated the catalog, the
//     push fails fast with this sentinel before the SDK is
//     invoked.
var (
	ErrNoAPIKey            = errors.New("paddle: cannot push usage without apiKey")
	ErrNegativeMBSeconds   = errors.New("paddle: negative mb_seconds")
	ErrOveragePriceMissing = errors.New("paddle: overage price missing for plan")
)

// flushOverageLocked is the cross-process claim state machine +
// SDK post for one (account, window) push. Stateless: no in-memory
// accumulator. Each call's `hour` argument is the meterd tick's
// timestamp; the window start is derived via
// windowStartFromHour (hour.UTC().Truncate(Hour), mirroring the
// stripe_push_dedupe grain).
//
// The PR #204-era Has/Record gate has been replaced by the
// pending/completed claim from migration 00037 + state.Store
// claim methods. The atomic claim is the durable guard against
// double-billing across processes (Has→POST→Record was a TOCTOU
// race that the per-process loop could not close). The window-
// scoped grain matches the meterd loop's UsageByHour read,
// closing the underbilling defect where the first positive
// window of the month POSTed but every subsequent window was
// skipped.
//
// Defensive zero-sum guard lives here too — flushOverageLocked is
// reachable from PushUsageRecord after the pre-SDK guards (which
// already short-circuit on 0) and from any future caller that
// wants to bypass the guards. Idempotent no-op on 0.
func (p *Provider) flushOverageLocked(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	if mbSeconds == 0 {
		return nil
	}
	windowStart := windowStartFromHour(hour.UTC())
	if p.dedupe != nil {
		claimed, err := p.dedupe.ClaimPaddleOverageWindow(ctx, acct.ID, windowStart, p.claimedBy(), paddleOverageLease)
		if err != nil {
			return fmt.Errorf("paddle: dedupe claim window=%s acct=%s: %w",
				windowStart.Format(time.RFC3339), acct.ID, err)
		}
		if !claimed {
			// Another pod holds a non-stale claim for this window.
			// Skip the SDK POST; the holder will Complete it.
			return nil
		}
	}
	flusher := p.flushFn
	if flusher == nil {
		flusher = defaultFlushLocked
	}
	if err := flusher(ctx, p, acct, windowStart, mbSeconds); err != nil {
		return err
	}
	if p.dedupe != nil {
		if err := p.dedupe.CompletePaddleOverageWindow(ctx, acct.ID, windowStart, mbSeconds); err != nil {
			return fmt.Errorf("paddle: dedupe complete window=%s acct=%s: %w",
				windowStart.Format(time.RFC3339), acct.ID, err)
		}
	}
	return nil
}

// windowStartFromHour returns the [start, start+1h) window
// containing t. Pulled out so the math is testable without driving
// the claim gate. Mirrors the stripe_push_dedupe grain at
// pkg/billing/stripe/client.go so the two providers share the same
// hourly windowing. UTC-normalized so a future caller that
// forgets to normalize cannot create a phantom row.
//
// (Note: PR #179-era code used calendarMonthStart here, which
// underbilled every account after the first positive window of the
// month because the meterd loop reads UsageByHour — window-scoped —
// but the dedupe row was keyed by month. The fix-PR rewrote this
// to per-window grain and added the claim state machine to make
// the cross-process race safe.)
func windowStartFromHour(t time.Time) time.Time {
	return t.Truncate(time.Hour)
}

// calendarMonthStart returns the first instant of t's UTC calendar
// month. Kept for backwards compatibility with the PR #179-era
// test fixtures that pin the math; the production dedupe grain is
// per-window (windowStartFromHour), but the merchant-dashboard
// CustomData["month"] still stamps the calendar-month string.
// Reference values: Feb 1, Mar 1, the leap-day edge
// (Feb 29 in leap years), and the Dec → Jan year boundary.
func calendarMonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// FlushFn is the seam flushOverageLocked delegates to. Each call
// builds the actual `CreateTransaction` SDK request and returns an
// error; a test stub can substitute a counter to drive cross-window
// flush semantics in unit tests without the SDK.
//
// Keep this signature stable — tests reach for it directly via
// provider.flushFn. The third argument's name is `windowStart` to
// match the per-window dedupe grain; semantically it's still a
// time.Time (callers that read calendarMonthStart semantics must
// update their assertions).
type FlushFn func(ctx context.Context, p *Provider, acct state.Account, windowStart time.Time, mbSeconds int64) error

// defaultFlushLocked is the production FlushFn: looks up the
// overage price handle for the account's plan, computes the
// integer wire quantity for the (account, window) push via
// billing.WireQuantityForMBSeconds, and posts a single Transactions
// line item with that quantity.
//
// Wire quantity: billing.WireQuantityForMBSeconds(mbSeconds). The
// same integer formula Stripe uses (pkg/billing/stripe/usage.go),
// shared via pkg/billing/plans.go so the two providers cannot drift.
// The overage price handle's UnitPrice.Amount is
// millicentsToPaddleAmount(billing.PlanOverageMillicentsPerGBHour())
// = "1" (one cent), so a push of N wire units bills N/1000 of a
// cent for the overage line — the unit-multiplication that was
// missing before this PR (Paddle was posting Quantity=1, which
// silently under-billed every account by ~250× for the canonical
// Hobby 24h case: 6_187 wire units vs 1).
//
// CustomData["mb_seconds"] is retained for the operator audit trail
// (the merchant dashboard renders the integer sum alongside the wire
// line item so a finance-ops reviewer can reconcile). The wire
// Quantity is the billable value; CustomData is documentation.
//
// Idempotency: the (acct, windowStart) Idempotency-Key is stamped via
// ContextWithTransitID (which the SDK forwards as X-Transit-Id on
// the outbound request); the RoundTripper at transport.go reads
// X-Transit-Id and copies it as Idempotency-Key on POST /transactions.
// Paddle's API server may not honor the header today (SDK team is
// working on native support); the header is observable on the
// wire for ops debugging and is forward-compat.
//
// CustomData["month"] is stamped with the calendar-month string
// (not the windowStart) so the merchant dashboard line items stay
// aggregated by month — this matches the operator-visible billing
// model and is independent of the per-window dedupe grain.
func defaultFlushLocked(ctx context.Context, p *Provider, acct state.Account, windowStart time.Time, mbSeconds int64) error {
	if mbSeconds < 0 {
		// Defensive guard — PushUsageRecord's pre-SDK guards already
		// short-circuit on negative mb_seconds (provider.go:319-321),
		// but a future caller that bypasses PushUsageRecord (tests,
		// backfills) would otherwise see WireQuantityForMBSeconds
		// truncate a negative toward zero and silently drop the line.
		// Use the same sentinel so the classifier at errors.go
		// renders the same Prometheus label as the entry-point path.
		return fmt.Errorf("%w (account %s, qty %d)", ErrNegativeMBSeconds, acct.ID, mbSeconds)
	}
	priceID := p.overagePriceForPlan(acct.Plan)
	if priceID == "" {
		return fmt.Errorf("%w (plan=%s)", ErrOveragePriceMissing, acct.Plan)
	}
	monthStr := calendarMonthString(windowStart)
	idem := fmt.Sprintf("faas-overage-%s-%s", acct.ID, windowStart.Format(time.RFC3339))
	customerID := acct.ProviderCustomerID // acct.ProviderCustomerID carries Stripe cus_… or Paddle ctm_… — same column, provider-discriminated by value shape per ADR-032.

	qty := billing.WireQuantityForMBSeconds(mbSeconds)

	// Stamp the transit ID on the context. The SDK's internal/client
	// (client.go:98-101) reads this and sets X-Transit-Id on the
	// outbound request; our transport wrapper copies it as
	// Idempotency-Key on POST /transactions. Single source of truth
	// for the idempotency value across the SDK header + our injected
	// header + the CustomData field.
	ctx = paddle.ContextWithTransitID(ctx, idem)

	req := &paddle.CreateTransactionRequest{
		CustomerID: &customerID,
		Items: []paddle.CreateTransactionItems{{
			TransactionItemFromCatalog: &paddle.TransactionItemFromCatalog{
				PriceID:  priceID,
				Quantity: int(qty),
			},
		}},
		CustomData: paddle.CustomData{
			"faas_account_id":      acct.ID,
			"month":                monthStr,
			"window_start":         windowStart.Format(time.RFC3339),
			"mb_seconds":           fmt.Sprintf("%d", mbSeconds),
			"faas_paddle_idem_key": idem,
		},
	}

	// Test-seam: append the wire-quantity row before the SDK POST so
	// a recorder-driven test observes what *would* be posted. nil
	// recorder → no-op. Mirrors the FlushFn stub (which intercepts
	// before the SDK is touched) without replacing the production
	// body — the seam answers a different question: not "what error
	// did the SDK return?" but "what quantity did we bill?".
	p.flushRecorderMu.Lock()
	if p.flushRecorder != nil {
		p.flushRecorder = append(p.flushRecorder, RecordedFlush{
			AccountID:   acct.ID,
			WindowStart: windowStart,
			MBSeconds:   mbSeconds,
			Quantity:    qty,
		})
	}
	p.flushRecorderMu.Unlock()

	_, err := p.client.CreateTransaction(ctx, req)
	if err != nil {
		return fmt.Errorf("paddle: CreateTransaction: %w", err)
	}
	return nil
}

// calendarMonthString returns the "2006-01" calendar-month string
// for a window-start timestamp. Used by defaultFlushLocked to
// stamp CustomData["month"] without re-doing the calendar math in
// every caller. UTC-normalized.
func calendarMonthString(t time.Time) string {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01")
}

// overagePriceForPlan returns the overage line-item price handle for
// a plan, from the priceCatalog.
func (p *Provider) overagePriceForPlan(plan api.Plan) string {
	p.catalog.mu.RLock()
	defer p.catalog.mu.RUnlock()
	return p.catalog.planOverage[plan]
}
