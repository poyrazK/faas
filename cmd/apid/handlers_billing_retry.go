// handlers_billing_retry.go — POST /v1/billing/retry (issue #242).
//
// Closes the customer-trust lie in the dunning email:
// `pkg/mail/account.go:107` (AccountSuspendedBody) and `:150`
// (PaymentFailedBody) promise the customer they can run
// `faas billing retry`; this handler is what the CLI calls.
//
// Provider dispatch (Stripe vs Paddle) goes through the
// billing.Provider interface — s.billingProvider is the seam. apid
// never sees provider-shaped JSON.
package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// postBillingRetry serves POST /v1/billing/retry. The account
// is the bearer-authenticated principal (the auth middleware
// resolves it before dispatch); no body, no path params.
//
// Error → Problem mapping:
//
//   - billing.ErrNoOpenCharge → 404 "no charge to retry" (the
//     account is in good standing; the dunning email was stale).
//   - billing.ErrNoAPIKey     → 502 "billing_retry_no_api_key"
//     (operator hasn't set the API key; CLI prints hint).
//   - billing.ErrNotImplemented → 501 "billing_retry_unsupported"
//     (the provider exposes payment-method recovery through its customer
//     portal instead; the response includes that portal URL when available).
//   - any other error          → 502 "billing_retry_failed",
//     with the SDK error wrapped in detail.
func (s *server) postBillingRetry(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.billingProvider == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
			api.CodeNotFound, "billing_disabled",
			"no billing provider configured on this box"))
		return
	}
	attemptID, refID, err := s.billingProvider.RetryLatestCharge(r.Context(), acct)
	if err != nil {
		s.log.Error("billing_retry",
			"account", acct.ID, "err", err)
		switch {
		case errors.Is(err, billing.ErrNoOpenCharge):
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound,
				api.CodeNotFound, "billing_no_open_charge",
				"the account is in good standing; no open invoice or transaction to retry"))
		case errors.Is(err, billing.ErrNoAPIKey):
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				"billing_retry_no_api_key", "billing provider mis-configured",
				"the operator has not configured a billing API key; contact support"))
		case errors.Is(err, billing.ErrNotImplemented):
			prob := api.NewProblem(http.StatusNotImplemented,
				"billing_retry_unsupported", "billing retry is not supported",
				"update your payment method in the billing portal and retry the charge there")
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), acct)
			api.WriteProblem(w, prob)
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				"billing_retry_failed", "billing retry failed", err.Error()))
		}
		return
	}
	writeJSON(w, http.StatusOK, api.BillingRetryResponse{
		AttemptID:     attemptID,
		ProviderRefID: refID,
		Status:        "pending_provider_confirmation",
		NextBillingAt: nil,
	})
}
