package statementinvoices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing/paddleinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

type paddleInvoiceIssue struct {
	export   state.StatementInvoiceExport
	exports  state.StatementInvoiceExportStore
	provider paddleProvider
	conn     connection
}

func (s *Service) paddleIssueContext(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (paddleInvoiceIssue, *api.Problem) {
	var issue paddleInvoiceIssue
	if scope.AccountID != acct.ID {
		return issue, productDocumentProblem(state.ErrNotFound)
	}
	stores, problem := s.productStores()
	if problem != nil {
		return issue, problem
	}
	doc, err := stores.documents.GetProductBillingDocument(ctx, scope, documentID)
	if err != nil {
		return issue, productDocumentProblem(err)
	}
	export, err := stores.exports.GetStatementInvoiceExport(ctx, acct.ID, state.StatementSourceProductControl, doc.ID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (doc.Kind != "statement" ||
		export.Provider != state.BillingProviderPaddle || export.Status != state.StatementInvoiceExportCompleted || export.ProviderInvoiceID == "")) {
		return issue, exportConflict("This statement has no completed Paddle draft export to issue.")
	}
	if err != nil {
		return issue, api.ErrInternal("could not load the statement invoice export")
	}
	provider := paddleProvider{client: s.paddleClient()}
	conn, problem := s.openConnection(ctx, stores.exports, acct.ID, state.BillingProviderPaddle, provider)
	if problem != nil {
		return issue, problem
	}
	if conn.livemode != export.Livemode {
		return issue, exportConflict("Reconnect Paddle in the mode that created this invoice.")
	}
	return paddleInvoiceIssue{export: export, exports: stores.exports, provider: provider, conn: conn}, nil
}

func (i paddleInvoiceIssue) transaction(ctx context.Context) (paddleinvoice.TransactionState, *api.Problem) {
	txn, err := i.provider.client.GetTransaction(ctx, i.conn.key, i.export.ProviderInvoiceID)
	if err != nil {
		return txn, paddleIssueCallProblem(err)
	}
	if txn.Deleted {
		return txn, exportConflict("Paddle no longer has this invoice. Check the statement's invoice in Paddle.")
	}
	if txn.ID != i.export.ProviderInvoiceID || txn.CustomerID != i.export.Customer || txn.Currency != i.export.Currency ||
		txn.CollectionMode != "manual" || txn.CustomData["gregale_export_id"] != i.export.ID ||
		txn.CustomData["gregale_statement_id"] != i.export.StatementID {
		return txn, exportConflict("The Paddle transaction no longer matches this statement's manually collected invoice.")
	}
	return txn, nil
}

func (s *Service) ReviewProductPaddleInvoiceIssue(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (ProductPaddleInvoiceIssueReview, *api.Problem) {
	var empty ProductPaddleInvoiceIssueReview
	payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !ok {
		return empty, api.ErrInternal("statement invoice payment status is unavailable")
	}
	issue, problem := s.paddleIssueContext(ctx, acct, scope, documentID)
	if problem != nil {
		return empty, problem
	}
	observedAt := s.now()
	txn, problem := issue.transaction(ctx)
	if problem != nil {
		return empty, problem
	}
	if paddleTransactionIssued(txn) {
		if _, problem := s.recordPaddleIssue(ctx, payments, issue, txn, observedAt); problem != nil {
			return empty, problem
		}
		return empty, exportConflict("Paddle has already issued this invoice. Its payment status has been refreshed.")
	}
	review, _, _, problem := issue.review(ctx, scope, txn)
	return review, problem
}

func (i paddleInvoiceIssue) review(ctx context.Context, scope state.ProductBillingScope, txn paddleinvoice.TransactionState) (
	ProductPaddleInvoiceIssueReview, paddleinvoice.Address, paddleinvoice.Business, *api.Problem,
) {
	var review ProductPaddleInvoiceIssueReview
	if txn.Status != "draft" && txn.Status != "ready" {
		return review, paddleinvoice.Address{}, paddleinvoice.Business{}, exportConflict("Only a Paddle draft or ready transaction can be issued.")
	}
	if !txn.GrandTotalKnown || txn.GrandTotal < 0 || txn.PaymentTermsFrequency < 1 || !paddleInvoiceTermsSupported(txn.PaymentTermsInterval) {
		return review, paddleinvoice.Address{}, paddleinvoice.Business{}, exportConflict("The Paddle draft needs a valid total and payment terms. Check the invoice in Paddle and review again.")
	}
	address, business, problem := i.provider.billingDetails(ctx, i.conn, i.export.Customer)
	if problem != nil {
		return review, address, business, problem
	}
	review = ProductPaddleInvoiceIssueReview{
		InvoiceID: txn.ID, Customer: txn.CustomerID, Currency: txn.Currency, AmountMinor: txn.GrandTotal,
		PaymentTermsInterval: txn.PaymentTermsInterval, PaymentTermsFrequency: txn.PaymentTermsFrequency, Livemode: i.export.Livemode,
	}
	raw, _ := json.Marshal(struct {
		Scope                                state.ProductBillingScope
		ExportID, DraftStatus                string
		TransactionUpdatedAt, ConnectionTime time.Time
		Address                              paddleinvoice.Address
		Business                             paddleinvoice.Business
		Terms                                ProductPaddleInvoiceIssueReview
	}{scope, i.export.ID, txn.Status, txn.UpdatedAt.UTC(), i.conn.updatedAt.UTC(), address, business, review})
	digest := sha256.Sum256(raw)
	review.Hash = hex.EncodeToString(digest[:])
	return review, address, business, nil
}

func (s *Service) IssueProductPaddleInvoice(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID, expected string) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	if len(expected) != sha256.Size*2 {
		return empty, paddleIssueReviewChanged()
	}
	store := s.Host.Store()
	payments, ok := store.(state.StatementInvoicePaymentStore)
	if !ok {
		return empty, api.ErrInternal("statement invoice payment status is unavailable")
	}
	issue, problem := s.paddleIssueContext(ctx, acct, scope, documentID)
	if problem != nil {
		return empty, problem
	}
	exports := issue.exports
	token := uuid.NewString()
	_, err := exports.ClaimStatementInvoiceExport(ctx, acct.ID, issue.export.ID, token, time.Now().Add(api.StatementInvoiceExportClaimTTL))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return empty, exportInProgress()
	}
	if err != nil {
		return empty, api.ErrInternal("could not claim the Paddle invoice")
	}
	defer func() {
		_ = exports.ReleaseStatementInvoiceExport(context.WithoutCancel(ctx), acct.ID, issue.export.ID, token)
	}()
	return s.issueReviewedPaddleInvoice(ctx, scope, issue, payments, expected)
}

func (s *Service) issueReviewedPaddleInvoice(ctx context.Context, scope state.ProductBillingScope,
	issue paddleInvoiceIssue, payments state.StatementInvoicePaymentStore, expected string) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	observedAt := s.now()
	txn, problem := issue.transaction(ctx)
	if problem != nil {
		return empty, problem
	}
	if paddleTransactionIssued(txn) {
		// A prior request may have issued the invoice and lost its response.
		return s.recordPaddleIssue(ctx, payments, issue, txn, observedAt)
	}
	review, address, business, problem := issue.review(ctx, scope, txn)
	if problem != nil {
		return empty, problem
	}
	if review.Hash != expected {
		return empty, paddleIssueReviewChanged()
	}
	if err := issue.provider.client.BillTransaction(ctx, issue.conn.key, txn.ID, address.ID, business.ID); err != nil {
		return empty, paddleIssueCallProblem(err)
	}
	observedAt = s.now()
	txn, problem = issue.transaction(ctx)
	if problem != nil {
		return empty, problem
	}
	if !paddleTransactionIssued(txn) {
		return empty, paddleIssueUnconfirmed()
	}
	return s.recordPaddleIssue(ctx, payments, issue, txn, observedAt)
}

func paddleInvoiceTermsSupported(interval string) bool {
	switch interval {
	case "day", "week", "month", "year":
		return true
	}
	return false
}

func paddleTransactionIssued(txn paddleinvoice.TransactionState) bool {
	switch txn.Status {
	case "billed", "past_due", "paid", "completed":
		return true
	}
	return false
}

func (s *Service) recordPaddleIssue(ctx context.Context, payments state.StatementInvoicePaymentStore,
	issue paddleInvoiceIssue, txn paddleinvoice.TransactionState, observedAt time.Time) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	payment, known, err := paddleTransactionPayment(txn)
	if err != nil || !known {
		return empty, paddleIssueUnconfirmed()
	}
	if err := s.recordInvoicePayment(ctx, payments, issue.export, payment, observedAt); err != nil {
		return empty, paddleIssueUnconfirmed()
	}
	current, err := issue.exports.GetStatementInvoiceExport(ctx, issue.export.AccountID, issue.export.SourceKind, issue.export.StatementID)
	if err != nil {
		return empty, paddleIssueUnconfirmed()
	}
	return statementInvoiceExportResponse(current), nil
}

func paddleIssueCallProblem(err error) *api.Problem {
	problem := paddleCallProblem(err)
	if problem.Code == api.CodeBillingProviderUnavailable {
		return paddleIssueUnconfirmed()
	}
	return problem
}

func paddleIssueUnconfirmed() *api.Problem {
	return providerProblem(http.StatusBadGateway, "Paddle issuance unconfirmed", "Could not confirm issuance. Retry to check the existing Paddle invoice.")
}

func paddleIssueReviewChanged() *api.Problem {
	return exportConflict("The Paddle draft, billing details, or connection changed. Review the invoice again.")
}
