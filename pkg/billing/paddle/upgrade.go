package paddle

import (
	"context"
	"fmt"

	"github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// CreateUpgradeTxnFn is the seam CreateUpgradeTransaction delegates to.
// Tests substitute a recorder stub so they can assert the SDK request
// shape (price handle, CustomData, Idempotency-Key tag) without standing
// up a full *paddle.SDK. nil → the production default body
// (defaultCreateUpgradeTxn), which issues the real
// paddle.Client.CreateTransaction call.
//
// Keep this signature stable — tests reach for it directly via
// provider.createUpgradeTxnFn.
type CreateUpgradeTxnFn func(ctx context.Context, p *Provider, acct state.Account, targetPlan api.Plan) (txnID, checkoutURL string, err error)

// CreateUpgradeTransaction materializes a Paddle-hosted checkout page for
// the upgrade to targetPlan. apid's changePlan handler calls this when
// the customer's plan is being upgraded and they have no active
// subscription item yet — the typical free → paid direct path
// (spec §4.7).
//
// Shape mirrors defaultFlushLocked (usage.go:127-158): same SDK call
// (paddle.Client.CreateTransaction), same CustomData tags, same column
// for the customer ID (acct.StripeCustomerID — the column is reused
// per ADR-025, the rename is a separate migration PR). The differences:
//
//   - priceID comes from planMonthly[plan] (the recurring subscription
//     price) instead of planOverage[plan] (the metered overage line).
//   - CustomData["kind"] = "plan_upgrade" so the merchant-dashboard
//     audit trail distinguishes upgrades from monthly overage flushes.
//   - CustomData["faas_paddle_idem_key"] is a stable (acct.ID, plan)
//     key so a redelivered upgrade click is collapsed by the merchant-
//     side dedupe (the paddle-go-sdk/v5@v5.2.0 SDK has no
//     Idempotency-Key request option, so we record it in CustomData;
//     HTTP-transport injection is a follow-up).
//
// Returns (txn_…, https://paddle.checkout/…, nil) on success. The 402
// Problem carries these as PaddleCheckoutURL + TxID extensions so the
// dashboard can render an upsell button + confirmation id.
func (p *Provider) CreateUpgradeTransaction(ctx context.Context, acct state.Account, targetPlan api.Plan) (string, string, error) {
	if p.client == nil {
		return "", "", fmt.Errorf("paddle: SDK not initialized")
	}
	fn := p.createUpgradeTxnFn
	if fn == nil {
		fn = defaultCreateUpgradeTxn
	}
	return fn(ctx, p, acct, targetPlan)
}

// defaultCreateUpgradeTxn is the production body. Looks up the per-plan
// monthly price handle from planMonthly[plan], posts a quantity-1
// Transactions line item with the (acct.ID, targetPlan) Idempotency-Key
// stamped into CustomData, and returns (txn.ID, *txn.Checkout.URL, nil).
//
// The "Checkout: nil" / "Checkout.URL: nil" guard below is a defense-
// in-depth check — the SDK contract guarantees a populated Checkout on
// a successful CreateTransaction, but if the wire format ever drifts
// we surface a typed error rather than rendering a 402 with an empty
// `paddle_checkout_url` extension (the dashboard would silently render
// a broken upsell button).
func defaultCreateUpgradeTxn(ctx context.Context, p *Provider, acct state.Account, targetPlan api.Plan) (string, string, error) {
	priceID := p.monthlyPriceForPlan(targetPlan)
	if priceID == "" {
		return "", "", fmt.Errorf("paddle: monthly price missing for plan=%s — EnsurePlanProducts must run first", targetPlan)
	}
	customerID := acct.StripeCustomerID // column name stale per ADR-025
	txn, err := p.client.CreateTransaction(ctx, &paddle.CreateTransactionRequest{
		CustomerID: &customerID,
		Items: []paddle.CreateTransactionItems{{
			TransactionItemFromCatalog: &paddle.TransactionItemFromCatalog{
				PriceID:  priceID,
				Quantity: 1,
			},
		}},
		CustomData: paddle.CustomData{
			"faas_account_id":      acct.ID,
			"target_plan":          string(targetPlan),
			"kind":                 "plan_upgrade",
			"faas_paddle_idem_key": fmt.Sprintf("faas-upgrade-%s-%s", acct.ID, targetPlan),
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("paddle: CreateTransaction: %w", err)
	}
	if txn == nil || txn.Checkout == nil || txn.Checkout.URL == nil || *txn.Checkout.URL == "" {
		return "", "", fmt.Errorf("paddle: CreateTransaction returned empty checkout for account=%s plan=%s", acct.ID, targetPlan)
	}
	return txn.ID, *txn.Checkout.URL, nil
}

// monthlyPriceForPlan returns the recurring-subscription price handle
// for a plan, from the priceCatalog. Mirrors overagePriceForPlan
// (usage.go:160-166) — same RWMutex + RLock shape. Looked up from
// planMonthly[plan] which EnsurePlanProducts populates with the
// pri_… handle Paddle returned for the per-plan monthly price.
func (p *Provider) monthlyPriceForPlan(plan api.Plan) string {
	p.catalog.mu.RLock()
	defer p.catalog.mu.RUnlock()
	return p.catalog.planMonthly[plan]
}
