package apidserver

import (
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/egressoverage"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// getEgressOverage serves GET /v1/account/egress-overage and
// GET /v1/orgs/{slug}/egress-overage (ADR-20261103131742 amendment 2).
func (s *server) getEgressOverage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.store.(state.EgressOverageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("egress overage choices are unavailable"))
		return
	}
	enabled, err := store.EgressOverageEnabled(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read the egress overage choice"))
		return
	}
	writeJSON(w, http.StatusOK, egressoverage.Response(acct.Plan, enabled, s.egressBilledLive(acct.Plan)))
}

// putEgressOverage serves PUT /v1/account/egress-overage and
// PUT /v1/orgs/{slug}/egress-overage. Every change is kept, so meterd bills
// an hour used while overage was on even after it is turned off.
func (s *server) putEgressOverage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.EgressOverageRequest
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", `send {"enabled": true} or {"enabled": false}`))
		return
	}
	if problem := egressoverage.Refusal(acct.Plan, *req.Enabled); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.EgressOverageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("egress overage choices are unavailable"))
		return
	}
	if err := store.SetEgressOverage(r.Context(), acct.ID, *req.Enabled, time.Now().UTC()); err != nil {
		api.WriteProblem(w, api.ErrInternal("could not save the egress overage choice"))
		return
	}
	s.audit.Emit(r.Context(), "billing.egress_overage_changed", &acct.ID, map[string]any{"enabled": *req.Enabled})
	writeJSON(w, http.StatusOK, egressoverage.Response(acct.Plan, *req.Enabled, s.egressBilledLive(acct.Plan)))
}

// egressBilledLive reports whether the billing provider bills egress live for
// plan.
func (s *server) egressBilledLive(plan api.Plan) bool {
	policies, ok := s.billingProvider.(billing.MeterUsagePolicyProvider)
	if !ok {
		return false
	}
	policy, configured := policies.MeterUsagePolicy(plan, state.BillingMeterEgress)
	return configured && policy.Mode == billing.MeterDeliveryLive
}
