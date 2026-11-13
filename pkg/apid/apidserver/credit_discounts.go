package apidserver

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/creditdiscount"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// wireCreditDiscounts applies account credit as a discount on the next order
// when the provider supports it (ADR-20261104212028); credits are then never
// refunded. The billing reconcile pass syncs every subject with credit.
func (s *server) wireCreditDiscounts() {
	s.creditDiscounts = nil
	s.billingSync.AfterPass = nil
	discounter, ok := s.billingProvider.(billing.CreditDiscounter)
	if !ok {
		return
	}
	store, ok := s.store.(creditdiscount.Store)
	if !ok {
		return
	}
	syncer := &creditdiscount.Syncer{
		Store: store, Provider: discounter, ProviderName: providerName(s.billingProvider), Log: s.log,
		Audit: func(ctx context.Context, event, subjectID string, fields map[string]any) {
			s.audit.Emit(ctx, event, &subjectID, fields)
		},
	}
	s.creditDiscounts = syncer
	s.billingSync.AfterPass = func(ctx context.Context, log *slog.Logger) {
		checked, failed, err := syncer.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			log.Warn("credit discounts: pass failed", "err", err)
		} else if failed > 0 {
			log.Info("credit discounts: pass", "checked", checked, "failed", failed)
		}
	}
}

// drawCreditDiscount draws an order's credit discount from the subject's
// credits. An error leaves the webhook delivery retryable; the ledger keeps
// one debit per order.
func (s *server) drawCreditDiscount(ctx context.Context, acct state.Account, inv *billing.InvoiceData) error {
	if s.creditDiscounts == nil || inv == nil {
		return nil
	}
	drawn, ok, err := creditdiscount.Draw(ctx, s.creditDiscounts.Store, s.creditDiscounts.ProviderName, acct.ID, inv)
	if errors.Is(err, creditdiscount.ErrOtherSubject) {
		s.log.Warn("credit discount: order carries another subject's discount", "account_id", acct.ID,
			"provider_discount_id", inv.ProviderDiscountID, "provider_invoice_id", inv.ProviderInvoiceID)
		return nil
	}
	if err != nil || !ok {
		return err
	}
	if !drawn.Result.AlreadyConsumedForInvoice {
		s.audit.Emit(ctx, "credit.applied", &acct.ID, map[string]any{
			"invoice_id":           drawn.InvoiceID,
			"provider":             s.creditDiscounts.ProviderName,
			"provider_discount_id": drawn.Discount.ProviderDiscountID,
			"amount_cents":         drawn.Result.ConsumedCents,
			"discount_cents":       inv.DiscountCents,
		})
		if drawn.Result.ConsumedCents < inv.DiscountCents {
			// Credit expired or was spent between attaching and the order.
			s.log.Warn("credit discount: order discount exceeds the credit left", "account_id", acct.ID,
				"discount_cents", inv.DiscountCents, "drawn_cents", drawn.Result.ConsumedCents)
		}
	}
	s.syncCreditDiscountAsync(ctx, acct.ID)
	return nil
}

// syncCreditDiscountAsync syncs subject's discount off the request path; the
// reconcile pass repeats a sync that fails.
//
//nolint:contextcheck // The sync intentionally outlives the request; it keeps the context values under its own timeout.
func (s *server) syncCreditDiscountAsync(parent context.Context, subjectID string) {
	syncer := s.creditDiscounts
	if syncer == nil || subjectID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
		defer cancel()
		if _, err := syncer.Sync(ctx, subjectID); err != nil {
			s.log.Warn("credit discount: sync", "subject", subjectID, "err", err)
		}
	}()
}
