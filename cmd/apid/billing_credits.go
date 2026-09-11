package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// applyInvoiceCredits turns the local goodwill-credit ledger into real money.
// It runs only for a paid provider invoice, drains credits idempotently against
// that invoice, then issues an equally sized provider refund using a stable
// key. A webhook retry therefore reuses both the ledger consumption and the
// provider refund instead of moving money twice.
func (s *server) applyInvoiceCredits(ctx context.Context, provider string, acct state.Account, data *billing.InvoiceData) error {
	if data == nil || data.ProviderInvoiceID == "" || data.AmountPaidCents <= 0 {
		return nil
	}
	inv, err := s.store.GetInvoiceByProviderID(ctx, acct.ID, provider, data.ProviderInvoiceID)
	if err != nil {
		return fmt.Errorf("load invoice for credits: %w", err)
	}
	_, _, err = s.consumeAndRefundInvoiceCredits(ctx, inv, "apid-webhook", "automatic invoice credit")
	return err
}

// consumeAndRefundInvoiceCredits is the only credit-consumption path. A local
// ledger decrement and its provider refund are coupled under one stable key;
// callers can retry after either network or projection failure without moving
// money twice. The provider's idempotency contract covers the external call,
// and invoice_refunds covers the local cumulative projection.
func (s *server) consumeAndRefundInvoiceCredits(ctx context.Context, inv state.Invoice, actor, reason string) (state.ConsumeAccountCreditResult, state.Invoice, error) {
	provider := providerName(s.billingProvider)
	if s.billingProvider == nil || !s.billingProvider.Capabilities().Has(billing.CapRefund) {
		return state.ConsumeAccountCreditResult{}, inv, fmt.Errorf("billing credits cannot be applied: provider %q has no refund capability", provider)
	}
	if provider == "" || provider != inv.Provider {
		return state.ConsumeAccountCreditResult{}, inv, fmt.Errorf("billing credits cannot be applied: invoice provider %q is not active", inv.Provider)
	}
	status := strings.ToLower(strings.TrimSpace(inv.Status))
	if status != "paid" && status != "partially_refunded" {
		return state.ConsumeAccountCreditResult{}, inv, fmt.Errorf("billing credits require a finalized paid invoice, got %q", inv.Status)
	}
	chargeID := inv.ProviderChargeID
	if chargeID == "" && provider == "polar" {
		// Polar's order is both the invoice projection and refundable handle.
		chargeID = inv.ProviderInvoiceID
	}
	if chargeID == "" {
		return state.ConsumeAccountCreditResult{}, inv, fmt.Errorf("billing credits cannot be applied: invoice has no refundable provider charge")
	}
	paid := inv.AmountPaidCents
	if paid <= 0 {
		paid = inv.TotalCents
	}
	refundable := paid - inv.AmountRefundedCents - inv.AmountRefundPendingCents
	if refundable < 0 {
		refundable = 0
	}
	consumed, inv, err := billing.ConsumeCreditsForInvoiceUpTo(
		ctx, s.store, inv.ID, refundable, actor, reason,
	)
	if err != nil {
		return state.ConsumeAccountCreditResult{}, inv, err
	}
	if consumed.ConsumedCents <= 0 {
		return consumed, inv, nil
	}
	idempotencyKey := "faas-credit-" + inv.ID
	refundCtx := billing.ContextWithIdempotencyKey(ctx, idempotencyKey)
	result, err := s.billingProvider.Refund(refundCtx, chargeID, consumed.ConsumedCents)
	if err != nil {
		return consumed, inv, fmt.Errorf("refund invoice credits: %w", err)
	}
	if result == nil || result.ProviderRefundID == "" {
		return consumed, inv, fmt.Errorf("refund invoice credits: provider returned no refund id")
	}
	if err := s.store.RecordInvoiceRefund(ctx, state.InvoiceRefund{
		InvoiceID:        inv.ID,
		ProviderRefundID: result.ProviderRefundID,
		IdempotencyKey:   idempotencyKey,
		AmountCents:      consumed.ConsumedCents,
		Source:           "credit",
		Status:           result.Status,
	}); err != nil {
		return consumed, inv, fmt.Errorf("record invoice credit refund: %w", err)
	}
	s.audit.Emit(ctx, "credit.applied", &inv.AccountID, map[string]any{
		"invoice_id":         inv.ID,
		"provider":           provider,
		"provider_refund_id": result.ProviderRefundID,
		"amount_cents":       consumed.ConsumedCents,
	})
	return consumed, inv, nil
}
