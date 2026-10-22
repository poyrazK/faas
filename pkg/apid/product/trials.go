package product

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operation"
	"github.com/onebox-faas/faas/pkg/state"
)

// PutCustomerTrial moves a customer's running trial to end at ends_at
// (ADR-20261021195901).
func (s *Service) PutCustomerTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.ProductTrialRequest
	if !operation.DecodeOperationBody(w, r, &req, api.ProductControlAuthBodyBytes) {
		return
	}
	s.setCustomerTrialEnd(w, r, acct, req.EndsAt, false)
}

// DeleteCustomerTrial ends a customer's running trial at the next UTC minute
// (ADR-20261021195901).
func (s *Service) DeleteCustomerTrial(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.setCustomerTrialEnd(w, r, acct, time.Now().UTC().Truncate(time.Minute).Add(time.Minute), true)
}

func (s *Service) setCustomerTrialEnd(w http.ResponseWriter, r *http.Request, acct state.Account, end time.Time, ended bool) {
	_, app, ok := s.owned(w, r, acct)
	if !ok {
		return
	}
	trials, ok := s.Host.Store().(state.ProductTrialStore)
	if !ok {
		respond(w, nil, state.ErrNotFound)
		return
	}
	// A previous apid would ignore the trial (ADR-20261021154511 release gate).
	if s.PlanPricingEnabled == nil || !s.PlanPricingEnabled() {
		respond(w, nil, errPlanPricingUnavailable)
		return
	}
	customer, err := trials.SetProductCustomerTrialEnd(r.Context(), acct.ID, app.ID, r.PathValue("tenant"), end)
	if err == nil {
		s.Host.EmitAudit(r.Context(), "product.customer.trial_changed", &acct.ID, map[string]any{"app_id": app.ID, "tenant_id": customer.TenantID,
			"trial_ends_at": customer.TrialEndsAt, "ended": ended})
	}
	respond(w, customer, err)
}
