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

func (s *Service) ReviewProductStripeInvoiceReminder(ctx context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID string) (ProductStripeInvoiceReminderReview, *api.Problem) {
	var empty ProductStripeInvoiceReminderReview
	issue, problem := s.stripeIssueContext(ctx, acct, scope, documentID)
	if problem != nil {
		return empty, problem
	}
	reminder, problem := issue.reminder(ctx)
	if problem != nil {
		return empty, problem
	}
	if !reminder.SentAt.IsZero() {
		return empty, exportConflict("Stripe already accepted the reminder for this invoice.")
	}
	if stripeReminderRetryExpired(reminder, s.now()) {
		return empty, stripeReminderNeedsReview()
	}
	review, problem := s.readStripeReminder(ctx, issue)
	if problem != nil {
		return empty, problem
	}
	if reminder.ReviewHash != "" && reminder.ReviewHash != review.Hash {
		return empty, exportConflict("The invoice changed after the reminder attempt. Check the invoice in Stripe.")
	}
	return review, nil
}

func (i stripeInvoiceIssue) reminder(ctx context.Context) (state.ProductStripeInvoiceReminder, *api.Problem) {
	attempt, problem := i.attempt(ctx)
	if problem != nil {
		return state.ProductStripeInvoiceReminder{}, problem
	}
	if attempt.Stage != state.StripeInvoiceSent {
		return state.ProductStripeInvoiceReminder{}, exportConflict("Confirm the initial Stripe send before reviewing a reminder.")
	}
	return attempt.Reminder, nil
}

func (s *Service) readStripeReminder(ctx context.Context, issue stripeInvoiceIssue) (ProductStripeInvoiceReminderReview, *api.Problem) {
	var empty ProductStripeInvoiceReminderReview
	payments, ok := s.Host.Store().(state.StatementInvoicePaymentStore)
	if !ok {
		return empty, api.ErrInternal("statement invoice payment status is unavailable")
	}
	observedAt := s.now()
	invoice, problem := issue.invoice(ctx)
	if problem != nil {
		return empty, problem
	}
	payment, known, err := stripeInvoicePayment(invoice)
	if err != nil || !known {
		return empty, providerProblem(http.StatusBadGateway, "Stripe payment status unavailable", "Retry to read the current Stripe invoice.")
	}
	if err := s.recordInvoicePayment(ctx, payments, issue.export, payment, observedAt); err != nil {
		return empty, api.ErrInternal("could not save current Stripe payment status")
	}
	if invoice.Status != state.InvoicePaymentOpen || invoice.DueDate == nil || !invoice.DueDate.Before(s.now()) ||
		payment.AmountRemainingMinor <= 0 {
		return empty, exportConflict("The Stripe invoice is no longer open and overdue.")
	}
	if invoice.CollectionMethod != "send_invoice" || invoice.AutoAdvance == nil || *invoice.AutoAdvance ||
		invoice.SendExportID != issue.export.ID {
		return empty, exportConflict("This invoice's Stripe collection settings changed. Check the invoice in Stripe.")
	}
	if !validStripeInvoiceRecipient(invoice.CustomerEmail) {
		return empty, exportConflict("Add a valid recipient email in Stripe before sending a reminder.")
	}
	review := ProductStripeInvoiceReminderReview{
		InvoiceID: invoice.ID, Recipient: invoice.CustomerEmail, Currency: issue.export.Currency,
		AmountRemainingMinor: payment.AmountRemainingMinor, DueAt: invoice.DueDate.UTC(), Livemode: issue.export.Livemode,
	}
	review.Hash = stripeIssueHash(struct {
		Account, Export, Customer string
		StripeAmountRemaining     int64
		Review                    ProductStripeInvoiceReminderReview
	}{issue.export.AccountID, issue.export.ID, invoice.Customer, invoice.AmountRemaining, review})
	return review, nil
}

func (s *Service) SendProductStripeInvoiceReminder(parent context.Context, acct state.Account, scope state.ProductBillingScope,
	documentID, expected string) (string, *api.Problem) {
	issue, problem := s.stripeIssueContext(parent, acct, scope, documentID)
	if problem != nil {
		return "", problem
	}
	token := uuid.NewString()
	account, id := issue.export.AccountID, issue.export.ID
	_, err := issue.exports.ClaimStatementInvoiceExport(parent, account, id, token, time.Now().Add(api.StatementInvoiceExportClaimTTL))
	if errors.Is(err, state.ErrStatementInvoiceExportInProgress) {
		return "", exportInProgress()
	}
	if err != nil {
		return "", api.ErrInternal("could not claim the Stripe invoice")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancel()
		_ = issue.exports.ReleaseStatementInvoiceExport(ctx, account, id, token)
	}()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 45*time.Second)
	defer cancel()
	if problem := s.sendReviewedStripeReminder(ctx, issue, token, expected); problem != nil {
		return "", problem
	}
	return issue.export.ProviderInvoiceID, nil
}

func (s *Service) sendReviewedStripeReminder(ctx context.Context, issue stripeInvoiceIssue, token, expected string) *api.Problem {
	reminder, problem := issue.reminder(ctx)
	if problem != nil {
		return problem
	}
	if reminder.ReviewHash != "" && reminder.ReviewHash != expected {
		return exportConflict("This invoice already has a reminder attempt. Check the invoice in Payment follow-up.")
	}
	if !reminder.SentAt.IsZero() {
		return nil
	}
	if stripeReminderRetryExpired(reminder, s.now()) {
		return stripeReminderNeedsReview()
	}
	review, problem := s.readStripeReminder(ctx, issue)
	if problem != nil {
		return problem
	}
	if review.Hash != expected {
		return exportConflict("The Stripe recipient, balance, or payment terms changed. Review the reminder again.")
	}
	now := s.now()
	if stripeReminderRetryExpired(reminder, now) {
		return stripeReminderNeedsReview()
	}
	account, id := issue.export.AccountID, issue.export.ID
	if reminder.StartedAt.IsZero() {
		if err := issue.sends.BeginProductStripeInvoiceReminder(ctx, account, id, token, expected, now); err != nil {
			return stripeReminderUnconfirmed()
		}
	}
	key := "gregale-product-invoice-reminder:" + id
	if _, err := issue.client.SendInvoice(ctx, issue.conn.key, key, issue.export.ProviderInvoiceID); err != nil {
		problem := stripeIssueCallProblem(err)
		if problem.Code == api.CodeBillingProviderUnavailable {
			return stripeReminderUnconfirmed()
		}
		return problem
	}
	if err := issue.sends.CompleteProductStripeInvoiceReminder(ctx, account, id, token, expected, s.now()); err != nil {
		return stripeReminderUnconfirmed()
	}
	s.Host.Audit(ctx, "statement_invoice.stripe_reminder_accepted", account, map[string]any{
		"export_id": id, "statement_id": issue.export.StatementID, "invoice_id": issue.export.ProviderInvoiceID, "livemode": issue.export.Livemode,
	})
	return nil
}

func stripeReminderRetryExpired(reminder state.ProductStripeInvoiceReminder, now time.Time) bool {
	return !reminder.StartedAt.IsZero() && !now.Before(reminder.StartedAt.Add(api.ProductStripeInvoiceSendRetryWindow))
}

func stripeReminderNeedsReview() *api.Problem {
	return exportConflict("The reminder retry window ended. Check the invoice's send history in Stripe.")
}

func stripeReminderUnconfirmed() *api.Problem {
	return providerProblem(http.StatusBadGateway, "Stripe reminder unconfirmed", "Retry to check the same Stripe reminder.")
}
