package statementinvoices

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// productStores are the stores a Product Control statement export reads.
type productStores struct {
	exports   state.StatementInvoiceExportStore
	documents state.ProductBillingStore
	tenants   state.PlatformTenantStore
}

func (s *Service) productStores() (productStores, *api.Problem) {
	store := s.Host.Store()
	exports, okExports := store.(state.StatementInvoiceExportStore)
	documents, okDocuments := store.(state.ProductBillingStore)
	tenants, okTenants := store.(state.PlatformTenantStore)
	if !okExports || !okDocuments || !okTenants {
		return productStores{}, api.ErrInternal("statement invoice exports are unavailable")
	}
	return productStores{exports: exports, documents: documents, tenants: tenants}, nil
}

// productDocumentProblem explains a failed Product Control document read.
func productDocumentProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Bad statement reference",
			"tenant_id, period and document must identify one Product Control statement")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such Product Control statement")
	}
	return api.ErrInternal("could not load the Product Control statement")
}

// ExportProductStatement exports one finalized Product Control statement to
// the account's own billing provider (ADR-20261018212918). The provider
// invoice ID becomes the statement's immutable handoff (ADR-1704), so a
// statement is either handed off by hand or exported, never both. Credit
// notes are not exported; they keep their recorded handoff.
func (s *Service) ExportProductStatement(ctx context.Context, acct state.Account, scope state.ProductBillingScope, documentID,
	provider, customer string) (api.StatementInvoiceExportResponse, *api.Problem) {
	if provider == "" {
		provider = state.BillingProviderStripe
	}
	if !state.ValidBillingProvider(provider) {
		return api.StatementInvoiceExportResponse{}, providerProblem(http.StatusUnprocessableEntity, "Unknown billing provider",
			"provider must be stripe, paddle or lago")
	}
	stores, problem := s.productStores()
	if problem != nil {
		return api.StatementInvoiceExportResponse{}, problem
	}
	src, problem := productStatementInvoiceSource(ctx, acct, stores, scope, documentID, customer)
	if problem != nil {
		return api.StatementInvoiceExportResponse{}, problem
	}
	export, problem := s.exportStatementInvoice(ctx, acct, stores.exports, src, provider)
	if problem != nil {
		return api.StatementInvoiceExportResponse{}, problem
	}
	return statementInvoiceExportResponse(export), nil
}

func productStatementInvoiceSource(ctx context.Context, acct state.Account, stores productStores, scope state.ProductBillingScope,
	documentID, requestedCustomer string) (statementInvoiceSource, *api.Problem) {
	doc, err := stores.documents.GetProductBillingDocument(ctx, scope, documentID)
	if err != nil {
		return statementInvoiceSource{}, productDocumentProblem(err)
	}
	invoice, err := billing.ProductControlStatementInvoice(doc)
	if err != nil {
		return statementInvoiceSource{}, statementInvoiceProblem(err)
	}
	tenant, err := stores.tenants.GetPlatformTenant(ctx, acct.ID, scope.TenantID)
	if err != nil {
		return statementInvoiceSource{}, productDocumentProblem(err)
	}
	start, err := time.Parse("2006-01", scope.Period)
	if err != nil {
		return statementInvoiceSource{}, productDocumentProblem(state.ErrInvalidArgument)
	}
	src := statementInvoiceSource{kind: state.StatementSourceProductControl, statementID: doc.ID, revision: 1,
		start: start, end: start.AddDate(0, 1, 0), requestedCustomer: requestedCustomer, externalRef: tenant.ExternalRef, invoice: invoice}
	handoff, err := stores.documents.GetProductBillingHandoff(ctx, scope, doc.ID)
	switch {
	case err == nil:
		src.existingHandoff = handoff.ExternalInvoiceID
	case !errors.Is(err, state.ErrNotFound):
		return statementInvoiceSource{}, api.ErrInternal("could not load the statement handoff")
	}
	src.handoff = func(ctx context.Context, invoiceID string) error {
		_, err := stores.documents.HandoffProductBilling(ctx, scope, doc.ID, invoiceID)
		return err
	}
	return src, nil
}

// GetProductStatementExport returns a Product Control statement's export,
// including its provider payment status (ADR-1504).
func (s *Service) GetProductStatementExport(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (api.StatementInvoiceExportResponse, *api.Problem) {
	stores, problem := s.productStores()
	if problem != nil {
		return api.StatementInvoiceExportResponse{}, problem
	}
	if _, err := stores.documents.GetProductBillingDocument(ctx, scope, documentID); err != nil {
		return api.StatementInvoiceExportResponse{}, productDocumentProblem(err)
	}
	export, err := stores.exports.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceProductControl, documentID)
	if errors.Is(err, state.ErrNotFound) {
		return api.StatementInvoiceExportResponse{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			"the statement has not been exported")
	}
	if err != nil {
		return api.StatementInvoiceExportResponse{}, api.ErrInternal("could not load the statement invoice export")
	}
	return statementInvoiceExportResponse(export), nil
}
