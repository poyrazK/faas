package statementinvoices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripeinvoice"
	"github.com/onebox-faas/faas/pkg/state"
)

type stripeInvoiceIssue struct {
	export  state.StatementInvoiceExport
	exports state.StatementInvoiceExportStore
	sends   state.ProductStripeInvoiceSendStore
	client  *stripeinvoice.Client
	conn    connection
}

func (s *Service) stripeIssueContext(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (stripeInvoiceIssue, *api.Problem) {
	var issue stripeInvoiceIssue
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
		export.Provider != state.BillingProviderStripe || export.Collection != state.CollectionNone ||
		export.Status != state.StatementInvoiceExportCompleted || export.ProviderInvoiceID == "")) {
		return issue, exportConflict("This statement has no completed Stripe draft export to send.")
	}
	if err != nil {
		return issue, api.ErrInternal("could not load the Stripe invoice export")
	}
	sends, ok := s.Host.Store().(state.ProductStripeInvoiceSendStore)
	if !ok {
		return issue, api.ErrInternal("Stripe invoice send status is unavailable")
	}
	client := s.stripeInvoiceClient()
	conn, problem := s.openConnection(ctx, stores.exports, acct.ID, state.BillingProviderStripe, stripeProvider{client: client})
	if problem != nil {
		return issue, problem
	}
	if conn.livemode != export.Livemode {
		return issue, exportConflict("Reconnect Stripe in the mode that created this invoice.")
	}
	return stripeInvoiceIssue{export: export, exports: stores.exports, sends: sends, client: client, conn: conn}, nil
}

func (i stripeInvoiceIssue) invoice(ctx context.Context) (stripeinvoice.InvoiceState, *api.Problem) {
	invoice, err := i.client.GetInvoice(ctx, i.conn.key, i.export.ProviderInvoiceID)
	if err != nil {
		return invoice, stripeIssueCallProblem(err)
	}
	if invoice.Deleted || invoice.ID != i.export.ProviderInvoiceID || invoice.Customer != i.export.Customer ||
		!strings.EqualFold(invoice.Currency, i.export.Currency) || invoice.Livemode == nil || *invoice.Livemode != i.export.Livemode ||
		invoice.SourceKind != state.StatementSourceProductControl ||
		invoice.StatementID != i.export.StatementID {
		return invoice, exportConflict("The Stripe invoice no longer matches this statement. Check the invoice in Stripe.")
	}
	return invoice, nil
}

func (i stripeInvoiceIssue) attempt(ctx context.Context) (state.ProductStripeInvoiceSend, *api.Problem) {
	attempt, err := i.sends.GetProductStripeInvoiceSend(ctx, i.export.AccountID, i.export.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return attempt, api.ErrInternal("could not load the Stripe invoice send")
	}
	return attempt, nil
}

func (s *Service) ReviewProductStripeInvoiceSend(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (ProductStripeInvoiceSendReview, *api.Problem) {
	return s.reviewProductStripeInvoiceSend(ctx, acct, scope, documentID, false)
}

func (s *Service) ReviewProductStripeDraftSend(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (ProductStripeInvoiceSendReview, *api.Problem) {
	return s.reviewProductStripeInvoiceSend(ctx, acct, scope, documentID, true)
}

func (s *Service) reviewProductStripeInvoiceSend(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string, draftOnly bool) (ProductStripeInvoiceSendReview, *api.Problem) {
	var empty ProductStripeInvoiceSendReview
	issue, problem := s.stripeIssueContext(ctx, acct, scope, documentID)
	if problem != nil {
		return empty, problem
	}
	attempt, problem := issue.attempt(ctx)
	if problem != nil {
		return empty, problem
	}
	if attempt.Stage == state.StripeInvoiceSent {
		return empty, exportConflict("Stripe has already accepted this invoice send.")
	}
	if draftOnly && attempt.Stage != "" {
		return empty, exportConflict("This invoice already has a pending send. Continue it from Payment follow-up.")
	}
	invoice, problem := issue.invoice(ctx)
	if problem != nil {
		return empty, problem
	}
	now := s.now()
	dueAt := attempt.DueAt
	if attempt.Stage == "" {
		dueAt = stripeInvoiceDueAt(invoice, issue.export.DaysUntilDue, now)
	}
	review, _, problem := issue.review(invoice, attempt, dueAt, now)
	return review, problem
}

func stripeInvoiceDueAt(invoice stripeinvoice.InvoiceState, days int, now time.Time) time.Time {
	if invoice.DueDate != nil && invoice.DueDate.After(now) {
		return *invoice.DueDate
	}
	return now.AddDate(0, 0, days).Truncate(24 * time.Hour)
}

func (i stripeInvoiceIssue) review(invoice stripeinvoice.InvoiceState, attempt state.ProductStripeInvoiceSend, dueAt, now time.Time) (
	ProductStripeInvoiceSendReview, string, *api.Problem,
) {
	var empty ProductStripeInvoiceSendReview
	if attempt.Stage == state.StripeInvoiceSending && !now.Before(attempt.SendStartedAt.Add(api.ProductStripeInvoiceSendRetryWindow)) {
		return empty, "", exportConflict("The safe retry window has ended. Use Check Stripe send in Payment follow-up.")
	}
	if !dueAt.After(now) {
		return empty, "", exportConflict("The reviewed payment deadline has passed. Check the invoice in Stripe.")
	}
	if invoice.AutoAdvance == nil || *invoice.AutoAdvance {
		return empty, "", exportConflict("This invoice must have automatic advancement disabled. Check the invoice in Stripe.")
	}
	unit, unitErr := billing.StripeMinorUnit(i.export.Currency)
	iso, isoOK := api.CurrencyMinorExponent(i.export.Currency)
	if !validStripeInvoiceRecipient(invoice.CustomerEmail) || unitErr != nil || !isoOK ||
		!invoice.HasTotals || invoice.Total < 0 || invoice.AmountDue <= 0 {
		return empty, "", exportConflict("The Stripe invoice needs a recipient email and a positive amount due before it can be sent.")
	}
	review := ProductStripeInvoiceSendReview{
		InvoiceID: invoice.ID, Recipient: invoice.CustomerEmail, Currency: i.export.Currency, Livemode: i.export.Livemode,
		TotalMinor: unit.ToISOMinor(invoice.Total, iso), AmountDueMinor: unit.ToISOMinor(invoice.AmountDue, iso), DueAt: dueAt.UTC(),
	}
	terms := stripeIssueHash(struct {
		Account, Export, Customer string
		Total, AmountDue          int64
		ConnectionTime            time.Time
		Review                    ProductStripeInvoiceSendReview
	}{i.export.AccountID, i.export.ID, invoice.Customer, invoice.Total, invoice.AmountDue, i.conn.updatedAt.UTC(), review})
	review.Hash = stripeIssueHash(struct {
		Terms, Collection string
		CurrentDue        *time.Time
	}{terms, invoice.CollectionMethod, invoice.DueDate})
	marker := invoice.SendExportID
	switch {
	case attempt.Stage == "" && (invoice.Status != "draft" || marker != ""):
		return empty, "", exportConflict("Only an unsent Stripe draft can start a reviewed send. Check the invoice in Stripe.")
	case attempt.Stage != "" && attempt.TermsHash != terms:
		return empty, "", exportConflict("The approved Stripe invoice details changed after sending began. Check the invoice in Stripe.")
	case attempt.Stage == state.StripeInvoicePreparing && marker == "" && invoice.Status == "draft" && attempt.ReviewHash == review.Hash:
		// A retry can still prepare an unchanged draft.
	case attempt.Stage != "":
		canResume := marker == i.export.ID && invoice.CollectionMethod == "send_invoice" && invoice.DueDate != nil && invoice.DueDate.Equal(dueAt) &&
			((invoice.Status == "draft" && attempt.Stage != state.StripeInvoiceSending) ||
				(invoice.Status == "open" && attempt.Stage != state.StripeInvoicePreparing))
		if !canResume {
			return empty, "", exportConflict("The Stripe invoice changed during the send. Check the invoice in Stripe.")
		}
		review.Hash = attempt.ReviewHash
	}
	return review, terms, nil
}

func validStripeInvoiceRecipient(email string) bool {
	address, err := mail.ParseAddress(email)
	return err == nil && address.Address == email
}

func stripeIssueHash(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (s *Service) SendProductStripeInvoice(parent context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID, expected string, dueAt time.Time) (api.StatementInvoiceExportResponse, *api.Problem) {
	var empty api.StatementInvoiceExportResponse
	issue, problem := s.stripeIssueContext(parent, acct, scope, documentID)
	if problem != nil {
		return empty, problem
	}
	payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !ok {
		return empty, api.ErrInternal("statement invoice payment status is unavailable")
	}
	token := uuid.NewString()
	_, err := issue.exports.ClaimStatementInvoiceExport(parent, acct.ID, issue.export.ID, token, time.Now().Add(api.StatementInvoiceExportClaimTTL))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return empty, exportInProgress()
	}
	if err != nil {
		return empty, api.ErrInternal("could not claim the Stripe invoice")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_ = issue.exports.ReleaseStatementInvoiceExport(ctx, acct.ID, issue.export.ID, token)
	}()

	// Finish recording the outcome after a browser disconnect, within the
	// caller's deadline if it is shorter than the single-send timeout.
	timeout := 2 * time.Minute
	if deadline, ok := parent.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), timeout)
	defer cancel()
	if problem := s.sendReviewedStripeInvoice(ctx, issue, token, expected, dueAt); problem != nil {
		return empty, problem
	}
	s.refreshStripeInvoicePayment(ctx, payments, issue)
	return statementInvoiceExportResponse(issue.export), nil
}

func (s *Service) refreshStripeInvoicePayment(ctx context.Context, payments state.StatementInvoicePaymentStore, issue stripeInvoiceIssue) {
	// The durable send outcome is authoritative. A later status sync can
	// refresh the payment fields if Stripe or the payment store is unavailable.
	observedAt := s.now()
	if invoice, problem := issue.invoice(ctx); problem == nil {
		if payment, known, err := stripeInvoicePayment(invoice); err == nil && known {
			_ = s.recordInvoicePayment(ctx, payments, issue.export, payment, observedAt)
		}
	}
}

func (s *Service) sendReviewedStripeInvoice(ctx context.Context, issue stripeInvoiceIssue, token, expected string, dueAt time.Time) *api.Problem {
	attempt, problem := issue.attempt(ctx)
	if problem != nil {
		return problem
	}
	if attempt.Stage != "" && (attempt.ReviewHash != expected || !attempt.DueAt.Equal(dueAt)) {
		return stripeSendReviewChanged()
	}
	if attempt.Stage == state.StripeInvoiceSent {
		return nil
	}
	invoice, problem := issue.invoice(ctx)
	if problem != nil {
		return problem
	}
	now := s.now()
	if attempt.Stage == "" && !dueAt.Equal(stripeInvoiceDueAt(invoice, issue.export.DaysUntilDue, now)) {
		return stripeSendReviewChanged()
	}
	review, terms, problem := issue.review(invoice, attempt, dueAt, now)
	if problem != nil {
		return problem
	}
	if review.Hash != expected {
		return stripeSendReviewChanged()
	}
	account, id := issue.export.AccountID, issue.export.ID
	if attempt.Stage == "" {
		attempt = state.ProductStripeInvoiceSend{Stage: state.StripeInvoicePreparing, ReviewHash: expected, TermsHash: terms, DueAt: dueAt}
		if err := issue.sends.BeginProductStripeInvoiceSend(ctx, account, id, token, attempt); err != nil {
			return stripeSendUnconfirmed()
		}
	}
	return s.dispatchStripeInvoiceSend(ctx, issue, invoice, token, attempt)
}

func (s *Service) dispatchStripeInvoiceSend(ctx context.Context, i stripeInvoiceIssue, invoice stripeinvoice.InvoiceState,
	token string, attempt state.ProductStripeInvoiceSend) *api.Problem {
	account, id := i.export.AccountID, i.export.ID
	key := "gregale-product-invoice-send:" + id
	if attempt.Stage == state.StripeInvoicePreparing {
		if invoice.SendExportID == "" {
			if err := i.client.PrepareInvoiceSend(ctx, i.conn.key, key+":prepare", invoice.ID, id, attempt.DueAt); err != nil {
				return stripeIssueCallProblem(err)
			}
		}
		if err := i.sends.AdvanceProductStripeInvoiceSend(ctx, account, id, token, state.StripeInvoiceFinalizing, s.now()); err != nil {
			return stripeSendUnconfirmed()
		}
		attempt.Stage = state.StripeInvoiceFinalizing
		var problem *api.Problem
		if invoice, problem = i.checkedStripeInvoice(ctx, attempt, s.now()); problem != nil {
			return problem
		}
	}
	if attempt.Stage == state.StripeInvoiceFinalizing {
		if invoice.Status == "draft" {
			if _, err := i.client.FinalizeManualInvoice(ctx, i.conn.key, key+":finalize", invoice.ID); err != nil {
				return stripeIssueCallProblem(err)
			}
		}
		now := s.now()
		attempt.Stage, attempt.SendStartedAt = state.StripeInvoiceSending, now
		var problem *api.Problem
		if invoice, problem = i.checkedStripeInvoice(ctx, attempt, now); problem != nil {
			return problem
		}
		if err := i.sends.AdvanceProductStripeInvoiceSend(ctx, account, id, token, state.StripeInvoiceSending, now); err != nil {
			return stripeSendUnconfirmed()
		}
	}
	if _, err := i.client.SendInvoice(ctx, i.conn.key, key+":send", invoice.ID); err != nil {
		return stripeIssueCallProblem(err)
	}
	if err := i.sends.AdvanceProductStripeInvoiceSend(ctx, account, id, token, state.StripeInvoiceSent, s.now()); err != nil {
		return stripeSendUnconfirmed()
	}
	s.Host.Audit(ctx, "statement_invoice.stripe_send_accepted", account, map[string]any{
		"export_id": id, "statement_id": i.export.StatementID, "invoice_id": invoice.ID, "livemode": i.export.Livemode,
	})
	return nil
}

func (i stripeInvoiceIssue) checkedStripeInvoice(ctx context.Context, attempt state.ProductStripeInvoiceSend, now time.Time) (stripeinvoice.InvoiceState, *api.Problem) {
	invoice, problem := i.invoice(ctx)
	if problem != nil {
		return invoice, problem
	}
	_, _, problem = i.review(invoice, attempt, attempt.DueAt, now)
	return invoice, problem
}

func stripeIssueCallProblem(err error) *api.Problem {
	if errors.Is(err, stripeinvoice.ErrKeyRejected) {
		return stripeCallProblem(err)
	}
	var stripeErr *stripeinvoice.Error
	if errors.As(err, &stripeErr) && stripeErr.Status >= 400 && stripeErr.Status < 500 &&
		stripeErr.Status != http.StatusTooManyRequests && stripeErr.Status != http.StatusConflict {
		return exportConflict("Stripe rejected the invoice operation. Check the existing invoice in Stripe.")
	}
	return stripeSendUnconfirmed()
}

func stripeSendUnconfirmed() *api.Problem {
	return providerProblem(http.StatusBadGateway, "Stripe send unconfirmed", "Retry to check the same Stripe invoice send.")
}

func stripeSendReviewChanged() *api.Problem {
	return exportConflict("The Stripe invoice or payment terms changed. Review the invoice again.")
}
