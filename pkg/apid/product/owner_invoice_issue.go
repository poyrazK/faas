package product

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) canIssueInvoice() bool {
	return s.ReviewInvoiceIssue != nil && s.IssueInvoice != nil
}

func (s *Service) reviewOwnerIssue(r *http.Request, c *ownerContext, action *ownerAction) error {
	if !s.canIssueInvoice() {
		return state.ErrNotFound
	}
	review, problem := s.ReviewInvoiceIssue(r.Context(), c.account, action.scope(c), action.Document)
	if problem != nil {
		return problem
	}
	action.Hash = review.Hash
	unit := review.PaymentTermsInterval
	if review.PaymentTermsFrequency != 1 {
		unit += "s"
	}
	confirmation := c.data.Confirmation
	confirmation.Title = "Issue Paddle invoice"
	confirmation.Description = "Paddle will issue and email this invoice using the customer's current billing details. Paddle determines the final tax."
	confirmation.SubmitLabel = "Issue and send invoice"
	confirmation.Details = []dashboard.ProductControlComparison{
		{Feature: "Paddle transaction", After: review.InvoiceID},
		{Feature: "Paddle customer", After: review.Customer},
		{Feature: "Draft total", After: ownerMinor(review.Currency, review.AmountMinor)},
		{Feature: "Payment terms", After: "Due " + strconv.Itoa(review.PaymentTermsFrequency) + " " + unit + " after issuance"},
		{Feature: "Mode", After: ownerInvoiceMode(review.Livemode)},
	}
	return nil
}

func (s *Service) canSendStripeInvoice() bool {
	return s.ReviewStripeInvoiceSend != nil && s.SendStripeInvoice != nil
}

func (s *Service) reviewOwnerStripeSend(r *http.Request, c *ownerContext, action *ownerAction) error {
	if !s.canSendStripeInvoice() {
		return state.ErrNotFound
	}
	review, problem := s.ReviewStripeInvoiceSend(r.Context(), c.account, action.scope(c), action.Document)
	if problem != nil {
		return problem
	}
	action.Hash, action.DueAt = review.Hash, review.DueAt.UTC().Format(time.RFC3339)
	confirmation := c.data.Confirmation
	confirmation.Title = "Send Stripe invoice"
	confirmation.Description = "Stripe emails this invoice for manual payment."
	if !review.Livemode {
		confirmation.Description = "Stripe records the send in test mode without emailing the recipient."
	}
	confirmation.SubmitLabel = "Send invoice"
	confirmation.Details = []dashboard.ProductControlComparison{
		{Feature: "Stripe invoice", After: review.InvoiceID},
		{Feature: "Recipient", After: review.Recipient},
		{Feature: "Amount due", After: ownerMinor(review.Currency, review.AmountDueMinor)},
		{Feature: "Due date", After: review.DueAt.UTC().Format("2006-01-02 15:04 UTC")},
		{Feature: "Mode", After: ownerInvoiceMode(review.Livemode)},
	}
	if review.TotalMinor != review.AmountDueMinor {
		confirmation.Details = append(confirmation.Details, dashboard.ProductControlComparison{Feature: "Invoice total", After: ownerMinor(review.Currency, review.TotalMinor)})
	}
	return nil
}

func (s *Service) canSendStripeReminder() bool {
	return s.ReviewStripeInvoiceReminder != nil && s.SendStripeInvoiceReminder != nil
}

func (s *Service) reviewOwnerStripeReminder(r *http.Request, c *ownerContext, action *ownerAction) error {
	if !s.canSendStripeReminder() {
		return state.ErrNotFound
	}
	review, problem := s.ReviewStripeInvoiceReminder(r.Context(), c.account, action.scope(c), action.Document)
	if problem != nil {
		return problem
	}
	action.Hash = review.Hash
	confirmation := c.data.Confirmation
	confirmation.Title = "Review Stripe payment reminder"
	confirmation.Description = "Stripe sends the overdue invoice and its payment link to the recipient."
	if !review.Livemode {
		confirmation.Description = "Stripe records the reminder in test mode without emailing the recipient."
	}
	confirmation.SubmitLabel = "Send reminder"
	confirmation.Details = []dashboard.ProductControlComparison{
		{Feature: "Stripe invoice", After: review.InvoiceID},
		{Feature: "Recipient", After: review.Recipient},
		{Feature: "Outstanding balance", After: ownerMinor(review.Currency, review.AmountRemainingMinor)},
		{Feature: "Due date", After: review.DueAt.UTC().Format("2006-01-02 15:04 UTC")},
		{Feature: "Mode", After: ownerInvoiceMode(review.Livemode)},
	}
	return nil
}

func (s *Service) renderOwnerIssueRetry(w http.ResponseWriter, r *http.Request, c *ownerContext, action, payload, token string, err error) bool {
	var problem *api.Problem
	if !errors.As(err, &problem) || (problem.Code != api.CodeBillingProviderUnavailable && problem.Code != api.CodeStatementInvoiceExportInProgress) {
		return false
	}
	title, label := "Check Paddle invoice issuance", "Retry issuance"
	switch action {
	case "send_stripe_invoice":
		title, label = "Check Stripe invoice send", "Retry send"
	case "send_stripe_reminder":
		title, label = "Check Stripe reminder", "Retry reminder"
	}
	c.data.Confirmation = &dashboard.ProductControlConfirmation{
		Title: title, Description: problem.Detail, SubmitLabel: label,
		Payload: payload, Token: token,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(problem.Status)
	s.OwnerRender(w, r, c.account, c.data)
	return true
}
