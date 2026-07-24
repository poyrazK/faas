package paddle

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// overageAccumulator is the per-account month-bucket that
// PushUsageRecord accumulates into. flushOverageLocked drains it as
// a flat-rate line item (quantity=1) with an Idempotency-Key
// recorded in the request's CustomData so the merchant dashboard
// audit trail includes a stable per-month identifier.
//
// Dedupe contract (reviewed in PR #158):
//
//   - Within-process, the `flushed` flag is the source of truth —
//     repeated calls in the same month become no-ops once the first
//     POST returns.
//   - Cross-process (apid restart between flush and `flushed=true`
//     stamp) is NOT covered today. PR #3's apid dispatch will route
//     the webhook handler's PushUsageRecord + meterd's pusher
//     through state.Store-backed dedupe (mirrors
//     pkg/stripex::HasStripePushHour / RecordStripePushHour — see
//     pkg/state/store.go:617 for the interface and
//     pkg/state/pgstore.go:1953 for the implementation).
//   - The paddle-go-sdk/v5@v5.2.0 SDK does NOT expose Idempotency-Key
//     as a request option. Today the idem key is recorded in
//     CustomData only; a HTTP-transport injection is the durable
//     fix and should land alongside the state-store dedupe.
type overageAccumulator struct {
	mu        sync.Mutex
	acct      state.Account
	month     time.Time // truncated to month via calendarMonthStart
	mbSeconds int64
	flushed   bool // set after a successful flushOverageLocked for this month
	lastFlush time.Time
}

// accumulateOverage inserts mb_seconds into the (acct, month) bucket.
// Cross-month boundary hands prior-month to flushOverageLocked
// (drained inline before the new bucket opens) — meter pushes one
// (acct, hour) per minute so the boundary case is rare but real.
//
// monthStart pins to the calendar month containing `hour` (UTC):
// Jan 31 23:59 + 1 minute → bucket key Feb 1 00:00 (different bucket,
// triggers a flush of Jan). The unit test
// TestAccumulateOverage_CrossMonthFlush exercises a Feb→Mar boundary
// that the original Truncate(30*24h) math got wrong in 28-/29-day months.
func (p *Provider) accumulateOverage(ctx context.Context, acct state.Account, hour time.Time, mbSeconds int64) error {
	monthStart := calendarMonthStart(hour.UTC())

	v, _ := p.pendingOverage.LoadOrStore(acct.ID, &overageAccumulator{
		acct:  acct,
		month: monthStart,
	})
	acc := v.(*overageAccumulator)
	acc.mu.Lock()
	defer acc.mu.Unlock()

	// If this mb_seconds falls into a different calendar month than
	// the accumulator's current month, drain the prior month first.
	if !acc.month.Equal(monthStart) && acc.mbSeconds > 0 && !acc.flushed {
		if err := p.flushOverageLocked(ctx, acc); err != nil {
			return fmt.Errorf("paddle: flush prior-month overage account=%s: %w", acct.ID, err)
		}
	}
	acc.acct = acct
	acc.month = monthStart
	acc.mbSeconds += mbSeconds
	return nil
}

// calendarMonthStart returns the first instant of t's UTC calendar
// month. Pulled out so the math is testable without driving the
// accumulator mutex. Reference values: Feb 1, Mar 1, the leap-day
// edge (Feb 29 in leap years), and the Dec → Jan year boundary.
//
// (Reviewer ask: the previous Truncate(30*24h).Truncate(time.Hour)
// only accidentally produced a month boundary on 30-day months —
// February pushed Feb 28 23:59 would bucket into the Jan 30 line,
// which then never flushed against Feb's actual month. The replaced
// function below is the correct one.)
func calendarMonthStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// flushOverageLocked posts the current bucket as a Paddle Transactions
// item with quantity 1 and the overage price handle from
// planOverage[plan]. Idempotent: a redelivered month gets the same
// Idempotency-Key so the SDK (and Paddle) collapse it.
//
// `p.flushFn` is the seam tests use to substitute a counter stub
// without standing up a full *paddle.SDK (the SDK has no recorder
// interface). Production paths never touch it. Same pattern as
// `time.Now` swappable, kept local to the file.
func (p *Provider) flushOverageLocked(ctx context.Context, acc *overageAccumulator) error {
	if acc.flushed {
		return nil
	}
	flusher := p.flushFn
	if flusher == nil {
		flusher = defaultFlushLocked
	}
	if err := flusher(ctx, p, acc); err != nil {
		return err
	}
	acc.flushed = true
	acc.lastFlush = p.now()
	return nil
}

// FlushFn is the seam `flushOverageLocked` delegates to. Each call
// builds the actual `CreateTransaction` SDK request and returns an
// error; a test stub can substitute a counter to drive cross-month
// flush semantics in unit tests without the SDK.
//
// Keep this signature stable — tests reach for it directly via
// provider.flushFn.
type FlushFn func(ctx context.Context, p *Provider, acc *overageAccumulator) error

// defaultFlushLocked is the production FlushFn: looks up the
// overage price handle for the account's plan, posts a quantity-1
// Transactions line item with the (acct, month) Idempotency-Key
// stamped into CustomData.
func defaultFlushLocked(ctx context.Context, p *Provider, acc *overageAccumulator) error {
	priceID := p.overagePriceForPlan(acc.acct.Plan)
	if priceID == "" {
		return fmt.Errorf("paddle: overage price missing for plan=%s — EnsurePlanProducts must run first", acc.acct.Plan)
	}
	idem := fmt.Sprintf("faas-overage-%s-%s", acc.acct.ID, acc.month.Format("2006-01"))
	qty := 1
	customerID := acc.acct.StripeCustomerID // column name stale per ADR-025
	_, err := p.client.CreateTransaction(ctx, &paddle.CreateTransactionRequest{
		CustomerID: &customerID,
		Items: []paddle.CreateTransactionItems{{
			TransactionItemFromCatalog: &paddle.TransactionItemFromCatalog{
				PriceID:  priceID,
				Quantity: qty,
			},
		}},
		CustomData: paddle.CustomData{
			"faas_account_id":      acc.acct.ID,
			"month":                acc.month.Format("2006-01"),
			"mb_seconds":           fmt.Sprintf("%d", acc.mbSeconds),
			"faas_paddle_idem_key": idem,
		},
	})
	if err != nil {
		return fmt.Errorf("paddle: CreateTransaction: %w", err)
	}
	return nil
}

// overagePriceForPlan returns the overage line-item price handle for
// a plan, from the priceCatalog.
func (p *Provider) overagePriceForPlan(plan api.Plan) string {
	p.catalog.mu.RLock()
	defer p.catalog.mu.RUnlock()
	return p.catalog.planOverage[plan]
}
