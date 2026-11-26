package product

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) canReviewInvoice() bool {
	return s.ReviewInvoice != nil && s.ConfirmInvoice != nil
}

func ownerInvoiceProvider(provider string) string {
	switch provider {
	case state.BillingProviderStripe:
		return "Stripe"
	case state.BillingProviderPaddle:
		return "Paddle"
	case state.BillingProviderLago:
		return "Lago"
	}
	return provider
}

func ownerInvoiceMode(livemode bool) string {
	if livemode {
		return "Live"
	}
	return "Test"
}

func ownerInvoiceQueueURL(c *ownerContext, period string) string {
	query := url.Values{"billing_month": {period}}
	return c.data.BaseURL + "?" + query.Encode() + "#awaiting-invoice"
}

func setOwnerInvoiceReturn(c *ownerContext, action ownerAction) {
	if action.Balances != nil {
		c.data.BalancesReturnURL = action.Balances.url(c)
	} else if action.Action == "issue_invoice" || action.Action == "send_stripe_invoice" || action.Action == "send_stripe_reminder" {
		c.data.PaymentQueueURL = c.data.BaseURL + "#payment-follow-up"
	}
	if action.Action == "finalize" || action.Action == "export_invoice" || action.Action == "handoff" {
		c.data.InvoiceQueueURL = ownerInvoiceQueueURL(c, action.Period)
	}
}

func (s *Service) reviewOwnerInvoice(r *http.Request, c *ownerContext, a *ownerAction) error {
	if !s.canReviewInvoice() {
		return state.ErrNotFound
	}
	review, problem := s.ReviewInvoice(r.Context(), c.account, a.scope(c), a.Document, a.Provider, a.InvoiceCustomer)
	if problem != nil {
		return problem
	}
	a.Provider, a.InvoiceCustomer, a.Hash = review.Provider, review.Customer, review.Hash
	collection := "Leave an invoice draft"
	switch review.Collection {
	case state.CollectionSend:
		collection = "Send the invoice, due in " + strconv.Itoa(review.DaysUntilDue) + " days"
	case state.CollectionCharge:
		collection = "Charge the customer's saved payment method"
	}
	confirmation := c.data.Confirmation
	confirmation.Title = "Invoice export"
	confirmation.Description = "Review where the invoice will go and how the provider will collect payment."
	confirmation.Details = []dashboard.ProductControlComparison{
		{Feature: "Statement", After: review.StatementID},
		{Feature: "Provider", After: ownerInvoiceProvider(review.Provider)},
		{Feature: "Provider customer", After: review.Customer},
		{Feature: "Mode", After: ownerInvoiceMode(review.Livemode)},
		{Feature: "Subtotal", After: ownerMinor(review.Currency, review.AmountMinor)},
		{Feature: "Collection", After: collection},
	}
	if review.AutomaticTax {
		confirmation.Details = append(confirmation.Details, dashboard.ProductControlComparison{Feature: "Tax", After: "The provider calculates tax"})
	}
	return nil
}

func (s *Service) ownerInvoiceQueue(r *http.Request, c *ownerContext, period, cursor string) error {
	store, ok := s.Host.Store().(state.ProductOwnerInvoiceQueueStore)
	if !ok {
		return nil
	}
	page, err := store.ListProductOwnerInvoiceQueue(r.Context(), c.account.ID, c.app.ID, period, cursor)
	if err != nil {
		return err
	}
	queue := &dashboard.ProductControlInvoiceQueue{}
	if len(page.Items) > 0 && s.canReviewInvoice() {
		queue.DraftProviders, err = s.ownerDraftInvoiceProviders(r.Context(), c.account.ID)
		if err != nil {
			return err
		}
	}
	for _, item := range page.Items {
		params := url.Values{"billing_month": {period}, "document": {item.DocumentID}}
		documentURL := ownerURL(c, item.TenantID, period, params)
		row := dashboard.ProductControlInvoiceQueueItem{
			TenantID: item.TenantID, DocumentID: item.DocumentID, Name: item.Name, Amount: ownerMoney(item.Currency, item.AmountMillicents),
			Provider: ownerInvoiceProvider(item.Provider), DocumentURL: documentURL + "#document-" + item.DocumentID,
		}
		if s.canReviewInvoice() {
			row.ExportURL = documentURL + "#export-" + item.DocumentID
		}
		if item.Provider == "" {
			row.HandoffURL = documentURL + "#handoff-" + item.DocumentID
		}
		queue.Items = append(queue.Items, row)
	}
	if page.NextCursor != "" {
		params := url.Values{"billing_month": {period}, "invoice_cursor": {page.NextCursor}}
		queue.NextURL = c.data.BaseURL + "?" + params.Encode() + "#awaiting-invoice"
	}
	c.data.BillingQueue.Invoices = queue
	return nil
}
