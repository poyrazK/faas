// Operator-facing provider refund surface.
//
// POST /v1/admin/accounts/{id}/refunds refunds a paid Polar order selected by
// its local Gregale invoice ID. The local invoice lookup is deliberate: it
// binds the provider order to the target account before any money-moving API
// call, so an operator cannot accidentally refund an order belonging to a
// different tenant.
package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

const maxBillingIdempotencyKeyLength = 255

type adminRefundPayload struct {
	InvoiceID   string `json:"invoice_id"`
	AmountCents int64  `json:"amount_cents"`
	Reason      string `json:"reason"`
}

// refundAccount handles POST /v1/admin/accounts/{id}/refunds.
//
// Refunds are deliberately MFA-gated at the route level, restricted to the
// operator email allowlist, and require an explicit Idempotency-Key. The
// provider receives that same key so a retry after an ambiguous network
// failure cannot create a second refund. The endpoint currently supports the
// Polar order identity stored in provider_invoice_id; other providers use
// different invoice/charge identities and must not be guessed here.
func (s *server) refundAccount(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}

	if s.billingProvider == nil || providerName(s.billingProvider) != "polar" ||
		!s.billingProvider.Capabilities().Has(billing.CapRefund) {
		api.WriteProblem(w, api.ErrBillingNotImplemented(
			"operator refunds are currently available for Polar invoice orders only"))
		return
	}

	targetID := r.PathValue("id")
	if _, err := uuid.Parse(targetID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad account id", "expected UUID"))
		return
	}

	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" || len(key) > maxBillingIdempotencyKeyLength {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad idempotency key", "Idempotency-Key is required and must be at most 255 characters"))
		return
	}

	var req adminRefundPayload
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad JSON", err.Error()))
		return
	}
	if _, err := uuid.Parse(req.InvoiceID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad invoice id", "invoice_id must be a UUID from GET /v1/invoices"))
		return
	}
	if req.AmountCents <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad amount", "amount_cents must be a positive integer"))
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if n := len(req.Reason); n < 3 || n > 500 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad reason", "reason must be 3..500 characters"))
		return
	}

	invoice, err := s.store.GetInvoiceByID(r.Context(), req.InvoiceID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Invoice not found", "the invoice does not exist"))
			return
		}
		s.log.Error("billing refund invoice lookup failed", "invoice_id", req.InvoiceID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not load invoice"))
		return
	}
	if invoice.AccountID != targetID || !strings.EqualFold(invoice.Provider, "polar") {
		// Do not reveal whether an invoice belongs to another account.
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"Invoice not found", "the invoice does not belong to this account"))
		return
	}
	if invoice.ProviderInvoiceID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Invoice cannot be refunded", "the invoice has no provider order identity"))
		return
	}
	status := strings.ToLower(strings.TrimSpace(invoice.Status))
	if status != "paid" && status != "partially_refunded" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Invoice cannot be refunded", "only a paid invoice can be refunded"))
		return
	}
	paidCents := invoice.AmountPaidCents
	if paidCents <= 0 {
		paidCents = invoice.TotalCents
	}
	if paidCents <= 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Invoice cannot be refunded", "the invoice has no paid amount"))
		return
	}
	if req.AmountCents > paidCents {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad amount", fmt.Sprintf("amount_cents cannot exceed the paid invoice amount of %d", paidCents)))
		return
	}

	refundCtx := billing.ContextWithIdempotencyKey(r.Context(), key)
	result, err := s.billingProvider.Refund(refundCtx, invoice.ProviderInvoiceID, req.AmountCents)
	if err != nil {
		s.log.Error("billing refund failed",
			"account_id", targetID,
			"invoice_id", req.InvoiceID,
			"provider_order_id", logsanitize.Field(invoice.ProviderInvoiceID),
			"amount_cents", req.AmountCents,
			"err", err)
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_refund_failed",
			"Billing refund failed", "the provider did not accept the refund: "+err.Error()))
		return
	}
	if result == nil || result.ProviderRefundID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "billing_refund_failed",
			"Billing refund failed", "the provider returned no refund identifier"))
		return
	}

	s.audit.Emit(r.Context(), "refund.requested", &targetID, map[string]any{
		"invoice_id":         req.InvoiceID,
		"provider":           invoice.Provider,
		"provider_refund_id": result.ProviderRefundID,
		"provider_order_id":  invoice.ProviderInvoiceID,
		"amount_cents":       result.AmountCents,
		"currency":           result.Currency,
		"status":             result.Status,
		"actor":              acct.ID,
		"actor_email":        acct.Email,
		"reason":             logsanitize.Field(req.Reason),
	})

	writeJSON(w, http.StatusOK, api.AdminRefundResponse{
		AccountID:        targetID,
		InvoiceID:        invoice.ID,
		Provider:         strings.ToLower(strings.TrimSpace(invoice.Provider)),
		ProviderRefundID: result.ProviderRefundID,
		ChargeID:         result.ChargeID,
		AmountCents:      result.AmountCents,
		Currency:         result.Currency,
		Status:           result.Status,
	})
}
