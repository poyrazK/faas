package statementinvoices

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	paymentRefreshTimeout = 25 * time.Second
	paymentRefreshLease   = paymentRefreshTimeout + 5*time.Second
	paymentRefreshReuse   = 10 * time.Second
)

// RefreshProductInvoicePayment checks one completed export in the owner's
// app and persists a confirmed provider observation through the usual sync path.
func (s *Service) RefreshProductInvoicePayment(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	if scope.AccountID != acct.ID {
		return empty, productDocumentProblem(state.ErrNotFound)
	}
	stores, problem := s.productStores()
	if problem != nil {
		return empty, problem
	}
	doc, err := stores.documents.GetProductBillingDocument(ctx, scope, documentID)
	if err != nil {
		return empty, productDocumentProblem(err)
	}
	if doc.Kind != "statement" {
		return empty, exportConflict("This statement has no completed invoice export to refresh.")
	}
	return s.refreshProductStatementPayment(ctx, stores.exports, acct.ID, doc.ID, "")
}

// RefreshProductHeldInvoicePayment uses the effective hold for the authorized
// workspace. The reference only matches the displayed hold, so a duplicate
// can never refresh the next invoice or select a different workspace's export.
func (s *Service) RefreshProductHeldInvoicePayment(ctx context.Context, acct state.Account, appID, tenantID, expectedHoldID string) *api.Problem {
	store, ok := s.Host.Store().(state.ProductOverdueStore)
	if !ok {
		return api.ErrInternal("Product Control overdue storage is unavailable")
	}
	hold, err := store.GetProductOverdueHold(ctx, acct.ID, appID, tenantID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return api.ErrInternal("could not load the payment hold")
	}
	if hold.ExportID != expectedHoldID {
		return nil
	}
	exports, ok := s.Host.Store().(state.StatementInvoiceExportStore)
	if !ok {
		return api.ErrInternal("statement invoice exports are unavailable")
	}
	_, problem := s.refreshProductStatementPayment(ctx, exports, acct.ID, hold.DocumentID, hold.ExportID)
	return problem
}

func (s *Service) refreshProductStatementPayment(ctx context.Context, exports state.StatementInvoiceExportStore,
	accountID, documentID, expectedExportID string) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !ok {
		return empty, api.ErrInternal("statement invoice payment status is unavailable")
	}
	export, err := exports.GetStatementInvoiceExport(ctx, accountID, state.StatementSourceProductControl, documentID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (export.Status != state.StatementInvoiceExportCompleted ||
		export.ProviderInvoiceID == "" || expectedExportID != "" && export.ID != expectedExportID)) {
		return empty, exportConflict("This statement has no completed invoice export to refresh.")
	}
	if err != nil {
		return empty, api.ErrInternal("could not load the statement invoice export")
	}
	if reuseInvoicePayment(export, s.now()) {
		return statementInvoiceExportResponse(export), nil
	}
	return s.refreshInvoicePayment(ctx, exports, payments, export)
}

func (s *Service) refreshInvoicePayment(parent context.Context, exports state.StatementInvoiceExportStore,
	payments state.StatementInvoicePaymentStore, export state.StatementInvoiceExport) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	p := s.provider(export.Provider)
	if p == nil {
		return empty, exportConflict("This invoice's billing provider cannot be refreshed.")
	}
	token := uuid.NewString()
	claimed, err := exports.ClaimStatementInvoiceExport(parent, export.AccountID, export.ID, token, time.Now().Add(paymentRefreshLease))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return empty, api.NewProblem(http.StatusConflict, api.CodeStatementInvoiceExportInProgress, "Payment refresh unavailable",
			"A payment refresh or invoice update is already in progress. Try again shortly.")
	}
	if err != nil {
		return empty, api.ErrInternal("could not claim the invoice for payment refresh")
	}
	release := true
	defer func() {
		if release {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
			defer cancel()
			_ = exports.ReleaseStatementInvoiceExport(ctx, export.AccountID, export.ID, token)
		}
	}()
	if reuseInvoicePayment(claimed, s.now()) {
		return statementInvoiceExportResponse(claimed), nil
	}

	// A repeated form submit can cancel the first browser request. Finish the
	// bounded provider read and save its result even if that browser leaves.
	timeout := paymentRefreshTimeout
	if deadline, ok := parent.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	conn, problem := s.openConnection(ctx, exports, claimed.AccountID, claimed.Provider, p)
	if problem != nil {
		if problem.Status == http.StatusConflict {
			return empty, exportConflict("Connect " + p.title() + " before refreshing this invoice.")
		}
		return empty, problem
	}
	if conn.livemode != claimed.Livemode {
		return empty, exportConflict("Reconnect " + p.title() + " in " + invoiceConnectionMode(claimed.Livemode) + " mode to check this invoice.")
	}

	observedAt := s.now()
	payment, known, err := lookupInvoicePayment(ctx, p, conn, claimed)
	if err != nil || !known {
		// Keep the short claim after a provider failure to coalesce retries.
		release = false
		if errors.Is(err, errKeyRejected) {
			return empty, exportConflict("Reconnect " + p.title() + " with a key that can read this invoice, then refresh again.")
		}
		if errors.Is(err, errInvoiceLookupUnconfirmed) {
			return empty, providerProblem(http.StatusBadGateway, "Payment status unavailable",
				p.title()+" could not find this invoice. Verify that the connected account can read it, then refresh again.")
		}
		return empty, providerProblem(http.StatusBadGateway, "Payment status unavailable",
			"Could not confirm payment status with "+p.title()+". Try again shortly.")
	}
	if err := s.recordInvoicePayment(ctx, payments, claimed, payment, observedAt); err != nil {
		return empty, api.ErrInternal("could not save the refreshed payment status")
	}
	current, err := exports.GetStatementInvoiceExport(ctx, claimed.AccountID, claimed.SourceKind, claimed.StatementID)
	if err != nil {
		return empty, api.ErrInternal("could not load the refreshed payment status")
	}
	return statementInvoiceExportResponse(current), nil
}

func invoiceConnectionMode(livemode bool) string {
	if livemode {
		return "live"
	}
	return "test"
}

func reuseInvoicePayment(export state.StatementInvoiceExport, now time.Time) bool {
	switch export.PaymentStatus {
	case state.InvoicePaymentPaid, state.InvoicePaymentVoid, state.InvoicePaymentDeleted:
		return true
	}
	return export.StatusSyncedAt != nil && now.Sub(*export.StatusSyncedAt) < paymentRefreshReuse
}
