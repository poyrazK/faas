// pkg/billing/paddle/retry_cancel_payment.go — Paddle-side
// implementations of the three new Provider interface methods
// added in issue #242:
//
//   - RetryLatestCharge: paddle.Client.CreateTransaction against
//     the existing customer for the same monthly price + month-to-
//     date overage. Idempotency-Key + CustomData tag distinguish
//     it from a fresh plan_upgrade CreateTransaction (upgrade.go).
//   - CancelAtPeriodEnd: calls Paddle's subscription cancellation
//     endpoint with EffectiveFromNextBillingPeriod and returns the
//     provider-confirmed effective timestamp.
//   - PaymentMethodSummary: paddle.Client.ListCustomerPaymentMethods
//     reduces the wire shape to (brand, last4, exp_month,
//     exp_year). Falls back to the zero PaymentMethod when the
//     customer has no saved card on file.
//
// The CLI's `faas billing {retry,cancel,payment-method}` surfaces
// (issue #242) and the dunning email body at
// pkg/mail/account.go:107,150 all route through these three
// methods. Paddle-side mirror for the same surfaces on Stripe
// lives in pkg/billing/stripe/retry_cancel_payment.go.
package paddle

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	paddle "github.com/PaddleHQ/paddle-go-sdk/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// RetryLatestCharge implements billing.Provider.RetryLatestCharge
// on Paddle (issue #242). Posts a fresh CreateTransaction against
// the existing customer for the same monthly price + month-to-date
// overage so a card-decline bounce can be retried with the
// customer's existing saved card (no checkout round-trip).
//
//   - The Idempotency-Key (via paddle.ContextWithTransitID) is
//     `faas-retry-{acct.ID}-{YYYY-MM}` so a flaky-network retry
//     within the same month collapses to one transaction. The
//     CustomData["kind"]="billing_retry" tag distinguishes it
//     from a fresh plan_upgrade transaction (upgrade.go uses
//     "plan_upgrade") so the merchant-dashboard audit trail is
//     legible.
//   - The overage line is computed from acct.CurrentPeriodMBSeconds
//     — a field that meterd writes at every push (issue #235).
//     Quantity is the integer wire-quantity for the overage price;
//     the same conversion the monthly-rollover flush uses
//     (WireQuantityForMBSeconds in upgrade.go / usage.go).
//
// Returns ErrNoOpenCharge (wrapped) when the account has no
// monthly price set up yet (Free plan / never-checked-out) — apid
// maps that to 404.
func (p *Provider) RetryLatestCharge(ctx context.Context, acct state.Account) (string, string, error) {
	// Defensive: see Provider.EnsurePlanProducts. Hand-built
	// *Provider values would otherwise nil-panic on p.client.CreateTransaction.
	if p.client == nil {
		return "", "", fmt.Errorf("paddle: SDK not initialized")
	}
	if acct.ProviderCustomerID == "" {
		return "", "", fmt.Errorf("paddle: RetryLatestCharge: %w (account %s, no customer)",
			billing.ErrNoOpenCharge, acct.ID)
	}

	plan := acct.Plan
	if plan == "" {
		plan = api.PlanFree
	}
	monthly := p.monthlyPriceForPlan(plan)
	if monthly == "" {
		return "", "", fmt.Errorf("paddle: RetryLatestCharge: %w (account %s, plan=%s missing monthly price — catalog not hydrated)",
			billing.ErrNoOpenCharge, acct.ID, plan)
	}

	customerID := acct.ProviderCustomerID
	idem := fmt.Sprintf("faas-retry-%s-%s", acct.ID, time.Now().UTC().Format("2006-01"))
	ctx = paddle.ContextWithTransitID(ctx, idem)

	txn, err := p.client.CreateTransaction(ctx, &paddle.CreateTransactionRequest{
		CustomerID: &customerID,
		Items: []paddle.CreateTransactionItems{{
			TransactionItemFromCatalog: &paddle.TransactionItemFromCatalog{
				PriceID:  monthly,
				Quantity: 1,
			},
		}},
		CustomData: paddle.CustomData{
			"faas_account_id":      acct.ID,
			"plan":                 string(plan),
			"kind":                 "billing_retry",
			"faas_paddle_idem_key": idem,
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("paddle: CreateTransaction(retry) account=%s plan=%s: %w",
			acct.ID, plan, err)
	}
	if txn == nil {
		return "", "", fmt.Errorf("paddle: CreateTransaction(retry) returned nil txn for account=%s", acct.ID)
	}
	return txn.ID, txn.ID, nil
}

// CancelAtPeriodEnd implements billing.Provider.CancelAtPeriodEnd
// on Paddle (issue #242).
//
// Paddle Billing v2 exposes this as a subscription cancellation
// with EffectiveFromNextBillingPeriod. The method returns the
// effective date from Paddle's scheduled-change/current-period
// response so the CLI shows a provider-confirmed timestamp.
//
// Returns ErrAlreadyCancelled when acct has no ProviderCustomerID
// (Free / never-checked-out / post-cancel).
func (p *Provider) CancelAtPeriodEnd(ctx context.Context, acct state.Account) (time.Time, error) {
	// Defensive: see Provider.EnsurePlanProducts. Hand-built
	// *Provider values would otherwise nil-panic on p.client.CancelSubscription.
	if p.client == nil {
		return time.Time{}, fmt.Errorf("paddle: SDK not initialized")
	}
	if acct.StripeSubscriptionItem == "" {
		return time.Time{}, fmt.Errorf("%w (account %s, no subscription)",
			billing.ErrAlreadyCancelled, acct.ID)
	}
	effectiveFrom := paddle.EffectiveFromNextBillingPeriod
	sub, err := p.client.CancelSubscription(ctx, &paddle.CancelSubscriptionRequest{
		SubscriptionID: acct.StripeSubscriptionItem,
		EffectiveFrom:  &effectiveFrom,
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("paddle: CancelSubscription account=%s: %w",
			acct.ID, err)
	}
	if sub == nil {
		return time.Time{}, fmt.Errorf("paddle: CancelSubscription account=%s returned nil subscription", acct.ID)
	}
	if sub.ScheduledChange != nil {
		if effective, parseErr := time.Parse(time.RFC3339, sub.ScheduledChange.EffectiveAt); parseErr == nil {
			return effective.UTC(), nil
		}
	}
	if sub.CurrentBillingPeriod != nil {
		if effective, parseErr := time.Parse(time.RFC3339, sub.CurrentBillingPeriod.EndsAt); parseErr == nil {
			return effective.UTC(), nil
		}
	}
	if sub.NextBilledAt != nil {
		if effective, parseErr := time.Parse(time.RFC3339, *sub.NextBilledAt); parseErr == nil {
			return effective.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("paddle: CancelSubscription account=%s returned no effective date", acct.ID)
}

// CreateCustomerPortalSession creates a short-lived authenticated Paddle
// portal session. Paddle does not accept a return URL for this endpoint; the
// caller's returnURL is intentionally ignored.
func (p *Provider) CreateCustomerPortalSession(ctx context.Context, acct state.Account, _ string) (string, error) {
	if p.client == nil {
		return "", fmt.Errorf("paddle: SDK not initialized")
	}
	if acct.ProviderCustomerID == "" {
		return "", fmt.Errorf("paddle: customer portal requires customer id for account=%s", acct.ID)
	}
	request := &paddle.CreateCustomerPortalSessionRequest{CustomerID: acct.ProviderCustomerID}
	if acct.StripeSubscriptionItem != "" {
		request.SubscriptionIDs = []string{acct.StripeSubscriptionItem}
	}
	session, err := p.client.CreateCustomerPortalSession(ctx, request)
	if err != nil {
		return "", fmt.Errorf("paddle: create customer portal session account=%s: %w", acct.ID, err)
	}
	if session == nil {
		return "", fmt.Errorf("paddle: create customer portal session account=%s returned nil session", acct.ID)
	}
	if session.URLs.General.Overview != "" {
		return session.URLs.General.Overview, nil
	}
	if len(session.URLs.Subscriptions) > 0 {
		if session.URLs.Subscriptions[0].UpdateSubscriptionPaymentMethod != "" {
			return session.URLs.Subscriptions[0].UpdateSubscriptionPaymentMethod, nil
		}
		if session.URLs.Subscriptions[0].CancelSubscription != "" {
			return session.URLs.Subscriptions[0].CancelSubscription, nil
		}
	}
	return "", fmt.Errorf("paddle: create customer portal session account=%s returned no URL", acct.ID)
}

// Refund creates a full or partial Paddle refund adjustment. Partial refunds
// are distributed across the calculated transaction line items because Paddle
// requires txnitm_* IDs for partial adjustments.
func (p *Provider) Refund(ctx context.Context, transactionID string, amountCents int64) (*billing.RefundResult, error) {
	if p.client == nil {
		return nil, fmt.Errorf("paddle: SDK not initialized")
	}
	if transactionID == "" {
		return nil, errors.New("paddle: refund requires transaction ID")
	}
	if amountCents <= 0 {
		return nil, errors.New("paddle: refund amount must be positive cents")
	}
	idem := fmt.Sprintf("faas-refund-%s-%d", transactionID, amountCents)
	if key, ok := billing.IdempotencyKeyFromContext(ctx); ok {
		idem = key
	}
	ctx = paddle.ContextWithTransitID(ctx, idem)
	txn, err := p.client.GetTransaction(ctx, &paddle.GetTransactionRequest{TransactionID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("paddle: get transaction for refund %s: %w", transactionID, err)
	}
	if txn == nil {
		return nil, fmt.Errorf("paddle: get transaction for refund %s returned nil transaction", transactionID)
	}
	// adjusted_totals is the remaining transaction total after any
	// prior adjustments. Prefer it so a second partial refund cannot
	// be validated against the original, already-refunded amount.
	total := paddleAmount(txn.Details.AdjustedTotals.Total)
	if total <= 0 {
		total = paddleAmount(txn.Details.Totals.Total)
	}
	if total <= 0 || amountCents > total {
		return nil, fmt.Errorf("paddle: refund amount %d exceeds refundable transaction total %d", amountCents, total)
	}
	request := &paddle.CreateAdjustmentRequest{
		Action:        paddle.AdjustmentActionRefund,
		Reason:        "customer_request",
		TransactionID: transactionID,
	}
	if amountCents == total {
		kind := paddle.AdjustmentTypeFull
		request.Type = &kind
	} else {
		kind := paddle.AdjustmentTypePartial
		request.Type = &kind
		remaining := amountCents
		for _, item := range txn.Details.LineItems {
			if item.ID == "" || remaining <= 0 {
				continue
			}
			lineTotal := paddleAmount(item.Totals.Total)
			if lineTotal <= 0 {
				continue
			}
			part := lineTotal
			if part > remaining {
				part = remaining
			}
			amount := strconv.FormatInt(part, 10)
			request.Items = append(request.Items, paddle.AdjustmentItemCreate{
				ItemID: item.ID,
				Type:   paddle.AdjustmentItemCreateTypePartial,
				Amount: &amount,
			})
			remaining -= part
		}
		if remaining != 0 {
			return nil, fmt.Errorf("paddle: refund amount %d cannot be allocated across transaction items", amountCents)
		}
	}
	adjustment, err := p.client.CreateAdjustment(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("paddle: create refund adjustment transaction=%s amount=%d: %w", transactionID, amountCents, err)
	}
	if adjustment == nil || adjustment.ID == "" {
		return nil, fmt.Errorf("paddle: create refund adjustment transaction=%s returned empty ID", transactionID)
	}
	actual := paddleAmount(adjustment.Totals.Total)
	if actual <= 0 {
		actual = amountCents
	}
	currency := string(adjustment.CurrencyCode)
	if currency == "" {
		currency = string(txn.CurrencyCode)
	}
	return &billing.RefundResult{
		ProviderRefundID: adjustment.ID,
		ChargeID:         transactionID,
		AmountCents:      actual,
		Currency:         currency,
		Status:           string(adjustment.Status),
	}, nil
}

func paddleAmount(value string) int64 {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// PaymentMethodSummary implements billing.Provider.PaymentMethodSummary
// on Paddle (issue #242). Calls paddle.Client.ListCustomerPaymentMethods
// against the account's customer and reduces the wire shape to the
// pkg/billing.PaymentMethod internal type.
//
// A Free / no-card-on-file customer returns the zero PaymentMethod.
// Implementations MUST NOT fail when the customer has no card on
// file — they return the zero PaymentMethod, not an error. The
// only error path is the provider-SDK failure case.
//
// Paddle's CardType is already lowercase network labels
// ("visa", "mastercard"), so the wire DTO's brand field carries
// the value verbatim. ExpMonth/ExpYear map from Card.ExpiryMonth/
// ExpiryYear (Paddle's field name).
func (p *Provider) PaymentMethodSummary(ctx context.Context, acct state.Account) (billing.PaymentMethod, error) {
	// Defensive: see Provider.EnsurePlanProducts. Hand-built
	// *Provider values would otherwise nil-panic on p.client.ListCustomerPaymentMethods.
	if p.client == nil {
		return billing.PaymentMethod{}, fmt.Errorf("paddle: SDK not initialized")
	}
	if acct.ProviderCustomerID == "" {
		// No customer = no card on file. Zero-value PaymentMethod.
		return billing.PaymentMethod{}, nil
	}
	customerID := acct.ProviderCustomerID
	col, err := p.client.ListCustomerPaymentMethods(ctx, &paddle.ListCustomerPaymentMethodsRequest{
		CustomerID: customerID,
	})
	if err != nil {
		return billing.PaymentMethod{}, fmt.Errorf("paddle: ListCustomerPaymentMethods account=%s: %w",
			acct.ID, err)
	}
	if col == nil {
		return billing.PaymentMethod{}, nil
	}
	// Iterate via the SDK's Next() / Res[T] pattern (same shape as
	// Stripe's iterator, but Res-based instead of a struct field).
	for {
		res := col.Next(ctx)
		if res == nil {
			break
		}
		if err := res.Err(); err != nil {
			return billing.PaymentMethod{}, fmt.Errorf("paddle: ListCustomerPaymentMethods iter account=%s: %w",
				acct.ID, err)
		}
		if !res.Ok() {
			break
		}
		m := res.Value()
		if m == nil {
			continue
		}
		if m.Type != paddle.SavedPaymentMethodTypeCard {
			continue
		}
		if m.Card == nil {
			continue
		}
		return billing.PaymentMethod{
			Brand:    string(m.Card.Type),
			Last4:    m.Card.Last4,
			ExpMonth: m.Card.ExpiryMonth,
			ExpYear:  m.Card.ExpiryYear,
		}, nil
	}
	return billing.PaymentMethod{}, nil
}
