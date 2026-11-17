package creditdiscount

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrOtherSubject reports an order carrying another subject's credit
// discount. Nothing is drawn; redelivery cannot change it.
var ErrOtherSubject = errors.New("credit discount belongs to another billing subject")

// Drawn is what an order's credit discount drew from the subject's credits.
type Drawn struct {
	Result state.ConsumeAccountCreditResult
	// Discount is Gregale's discount the order carried.
	Discount state.CreditDiscount
	// InvoiceID is the local invoice of the order.
	InvoiceID string
}

// Draw draws the discount the provider applied to an order from the
// subject's credits, when the discount is Gregale's credit discount. It runs
// for every order delivery, paid or not: the provider reduced the order when
// it was created. The credit ledger keeps one debit per order, so a
// redelivery draws nothing more. ok is false for an order without a credit
// discount. The order's invoice projection must already be stored.
func Draw(ctx context.Context, store Store, provider string, subjectID string, inv *billing.InvoiceData) (Drawn, bool, error) {
	if inv == nil || inv.ProviderDiscountID == "" || inv.DiscountCents <= 0 || inv.ProviderInvoiceID == "" {
		return Drawn{}, false, nil
	}
	d, err := store.CreditDiscountByProviderID(ctx, provider, inv.ProviderDiscountID)
	if errors.Is(err, state.ErrNotFound) {
		return Drawn{}, false, nil
	}
	if err != nil {
		return Drawn{}, false, fmt.Errorf("credit discount: read %s: %w", inv.ProviderDiscountID, err)
	}
	if d.SubjectID != subjectID {
		return Drawn{}, false, fmt.Errorf("credit discount %s: %w", inv.ProviderDiscountID, ErrOtherSubject)
	}
	invoice, err := store.GetInvoiceByProviderID(ctx, subjectID, provider, inv.ProviderInvoiceID)
	if err != nil {
		return Drawn{}, false, fmt.Errorf("credit discount: order invoice: %w", err)
	}
	res, err := store.ConsumeAccountCredit(ctx, state.ConsumeAccountCreditParams{
		AccountID:         subjectID,
		TargetCents:       min(inv.DiscountCents, d.AmountCents),
		Provider:          provider,
		ProviderInvoiceID: inv.ProviderInvoiceID,
		InvoiceID:         invoice.ID,
		Reason:            "credit discount " + d.ProviderDiscountID,
		Actor:             "apid-webhook",
	})
	if err != nil {
		return Drawn{}, false, fmt.Errorf("credit discount: draw credits: %w", err)
	}
	if _, err := store.RedeemCreditDiscount(ctx, d.ID, inv.ProviderInvoiceID, res.ConsumedCents); err != nil {
		return Drawn{}, false, fmt.Errorf("credit discount: redeem: %w", err)
	}
	return Drawn{Result: res, Discount: d, InvoiceID: invoice.ID}, true, nil
}
