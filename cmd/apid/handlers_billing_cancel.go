// handlers_billing_cancel.go — POST /v1/billing/cancel (issue #242).
//
// Sets cancel_at_period_end on the account's subscription. Account
// keeps running until period end, then downgrades to Free
// (spec §4.7).
//
// The destructive nature of the action is gated on the CLI side
// (typed-confirm from PR #782: "cancel subscription"). apid itself
// does not gate — headless callers can wire the endpoint behind
// their own confirm dialog. The 409 response on re-cancel is the
// idempotency signal so a redelivery does not surface as a server
// error.
//
// Provider dispatch (Stripe vs Paddle) goes through the
// billing.Provider interface — s.billingProvider is the seam.
// Stripe: Subscriptions.Update(cancel_at_period_end=true).
// Paddle: Subscriptions.Cancel with
// EffectiveFromNextBillingPeriod.
package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// postBillingCancel serves POST /v1/billing/cancel. The account
// is the bearer-authenticated principal; no body, no path params.
//
// Error → Problem mapping:
//
//   - billing.ErrAlreadyCancelled → 409 "billing_already_cancelled"
//     (no active subscription; CLI renders a friendly hint).
//   - billing.ErrNoAPIKey         → 502 "billing_cancel_no_api_key".
//   - any other error              → 502 "billing_cancel_failed".
func (s *server) postBillingCancel(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.billingProvider == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
			api.CodeNotFound, "billing_disabled",
			"no billing provider configured on this box"))
		return
	}
	effectiveAt, err := s.billingProvider.CancelAtPeriodEnd(r.Context(), acct)
	if err != nil {
		s.log.Error("billing_cancel",
			"account", acct.ID, "err", err)
		switch {
		case errors.Is(err, billing.ErrAlreadyCancelled):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict,
				api.CodeConflict, "billing_already_cancelled",
				"this account has no active subscription to cancel"))
		case errors.Is(err, billing.ErrNoAPIKey):
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				"billing_cancel_no_api_key", "billing provider mis-configured",
				"the operator has not configured a billing API key; contact support"))
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				"billing_cancel_failed", "billing cancel failed", err.Error()))
		}
		return
	}
	writeJSON(w, http.StatusOK, api.BillingCancelResponse{
		CancelScheduled: true,
		EffectiveAt:     effectiveAt,
	})
}
