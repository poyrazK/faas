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
// PushUsageRecord accumulates into. FlushOverageNow drains it as
// a flat-rate line item (quantity=1) with an Idempotency-Key
// derived from the bucket. The meterd quota + dunning timers
// call FlushOverageNow on every loop tick so a misbehaving
// timer still works — the per-(acct, month) idem-key collapses
// duplicates.
type overageAccumulator struct {
	mu        sync.Mutex
	acct      state.Account
	month     time.Time // truncated to month
	mbSeconds int64
	flushed   bool // set after a successful FlushOverageNow for this month
	lastFlush time.Time
}

// accumulateOverage inserts mb_seconds into the (acct, hour-month)
// bucket. Cross-month boundary hands prior-month to flushOverageNow
// (drained inline before the new bucket opens) — meter pushes one
// (acct, hour) per minute so the boundary case is rare but real.
func (p *Provider) accumulateOverage(acct state.Account, hour time.Time, mbSeconds int64) error {
	monthStart := hour.UTC().Truncate(30 * 24 * time.Hour).Truncate(time.Hour) // floor at hour

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
		if err := p.flushOverageLocked(context.Background(), acc); err != nil {
			return fmt.Errorf("paddle: flush prior-month overage account=%s: %w", acct.ID, err)
		}
	}
	acc.acct = acct
	acc.month = monthStart
	acc.mbSeconds += mbSeconds
	return nil
}

// flushOverageLocked posts the current bucket as a Paddle Transactions
// item with quantity 1 and the overage price handle from
// planOverage[plan]. Idempotent: a redelivered month gets the same
// Idempotency-Key so the SDK (and Paddle) collapse it.
func (p *Provider) flushOverageLocked(ctx context.Context, acc *overageAccumulator) error {
	if acc.flushed {
		return nil
	}
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
	acc.flushed = true
	acc.lastFlush = p.now()
	return nil
}

// overagePriceForPlan returns the overage line-item price handle for
// a plan, from the priceCatalog.
func (p *Provider) overagePriceForPlan(plan api.Plan) string {
	p.catalog.mu.RLock()
	defer p.catalog.mu.RUnlock()
	return p.catalog.planOverage[plan]
}
