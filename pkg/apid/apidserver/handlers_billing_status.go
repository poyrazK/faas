package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// getBillingStatus serves the customer-authenticated, provider-independent
// billing status surface. The authenticated account supplied by authLimited is
// the only tenant input; no operator endpoint or account id is consulted.
func (s *server) getBillingStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	mode := s.billingMode.Effective()
	resolved := acct
	if mode.Enabled() {
		var err error
		resolved, err = s.accountForActiveBillingProvider(r.Context(), acct)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load billing status"))
			return
		}
	}

	name := s.billingProviderName
	if name == "" {
		name = providerName(s.billingProvider)
	}
	reconciliation := s.billingProvider != nil &&
		s.billingProvider.Capabilities().Has(billing.CapUsageReconcile)
	enabled := mode.Enabled()
	if !enabled {
		reconciliation = false
	}
	out := api.BillingStatusResponse{
		Mode:                       string(mode),
		Enabled:                    enabled,
		Provider:                   name,
		Plan:                       resolved.Plan,
		AccountStatus:              string(resolved.Status),
		CustomerConfigured:         resolved.ProviderCustomerID != "",
		SubscriptionConfigured:     resolved.StripeSubscriptionItem != "",
		UsageReconciliationEnabled: reconciliation,
	}
	// A scheduled cancellation or downgrade is visible before it applies
	// (ADR-20261103131742 amendment 1); a failed read leaves it out.
	if enabled {
		if current, ok := s.subscriptionState(r.Context(), resolved); ok {
			scheduledChangeFields(&out, current)
		}
	}
	writeJSON(w, http.StatusOK, out)
}
