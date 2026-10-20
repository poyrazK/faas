package product

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// errDocumentExported refuses a manual handoff for a statement whose export
// has started: the export records its provider invoice as the handoff
// (ADR-20261018212918).
var errDocumentExported = fmt.Errorf("%w: statement exported to a billing provider", state.ErrConflict)

// withInvoices attaches each statement's export and payment status
// (ADR-20261018212918). Customers see completed exports only.
func (s *Service) withInvoices(ctx context.Context, accountID string, docs *api.ProductBillingDocuments, owner bool) error {
	if s.DocumentInvoice == nil {
		return nil
	}
	for i := range docs.Documents {
		doc := &docs.Documents[i]
		if doc.Kind != "statement" {
			continue
		}
		export, err := s.DocumentInvoice(ctx, accountID, doc.ID)
		if err != nil {
			return err
		}
		doc.Invoice = documentInvoice(export, owner)
	}
	return nil
}

// documentInvoice projects an export for an owner or a customer. The hosted
// link is kept only as the provider's own https page; Gregale never renders
// payment forms.
func documentInvoice(e *api.StatementInvoiceExportResponse, owner bool) *api.ProductDocumentInvoice {
	if e == nil || (!owner && e.Status != state.StatementInvoiceExportCompleted) {
		return nil
	}
	out := &api.ProductDocumentInvoice{PaymentStatus: e.PaymentStatus, Overdue: e.Overdue, Currency: e.Currency, AmountMinor: e.AmountMinor,
		AmountRemainingMinor: e.AmountRemainingMinor, DueAt: e.DueAt, PaidAt: e.PaidAt}
	if e.AmountRemainingMinor != nil {
		remaining := api.MinorToMillicents(e.Currency, *e.AmountRemainingMinor)
		out.AmountRemainingMillicents = &remaining
	}
	out.HostedInvoiceURL = api.ProviderHostedURL(e.HostedInvoiceURL)
	if owner {
		out.Provider, out.InvoiceID, out.DashboardURL, out.ExportStatus = e.Provider, e.InvoiceID, e.DashboardURL, e.Status
	}
	return out
}

// handoffManually records an owner's external invoice reference unless the
// statement's export has started.
func (s *Service) handoffManually(ctx context.Context, billing state.ProductBillingStore, accountID string, scope state.ProductBillingScope,
	document, reference string) (api.ProductInvoiceHandoff, error) {
	if s.DocumentInvoice != nil {
		if _, err := billing.GetProductBillingDocument(ctx, scope, document); err != nil {
			return api.ProductInvoiceHandoff{}, err
		}
		export, err := s.DocumentInvoice(ctx, accountID, document)
		if err != nil {
			return api.ProductInvoiceHandoff{}, err
		}
		if export != nil {
			return api.ProductInvoiceHandoff{}, errDocumentExported
		}
	}
	return billing.HandoffProductBilling(ctx, scope, document, reference)
}
