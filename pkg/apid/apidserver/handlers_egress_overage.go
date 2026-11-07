package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/egressoverage"
	"github.com/onebox-faas/faas/pkg/state"
)

// getEgressOverage serves GET /v1/account/egress-overage and
// GET /v1/orgs/{slug}/egress-overage: the billing subject's egress overage
// election (ADR-20261104204500).
func (s *server) getEgressOverage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	elections, ok := s.store.(state.EgressOverageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("egress overage elections are unavailable"))
		return
	}
	election, err := elections.EgressOverageElection(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read egress overage election"))
		return
	}
	writeJSON(w, http.StatusOK, egressoverage.View(s.billingProvider, acct.Plan, election, time.Now().UTC()))
}

// putEgressOverage serves PUT /v1/account/egress-overage and
// PUT /v1/orgs/{slug}/egress-overage: {"enabled": true|false}. Turning it on
// needs a paid plan (402 plan_egress_overage_not_allowed); turning it off is
// always allowed. Audit row egress_overage.changed records each change.
func (s *server) putEgressOverage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.EgressOverageRequest
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		detail := "enabled (true or false) is required"
		if err != nil {
			detail = err.Error()
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", detail))
		return
	}
	elections, ok := s.store.(state.EgressOverageStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("egress overage elections are unavailable"))
		return
	}
	before, err := elections.EgressOverageElection(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read egress overage election"))
		return
	}
	now := time.Now().UTC()
	after, err := egressoverage.Set(r.Context(), elections, acct.ID, acct.Plan, *req.Enabled, now)
	switch {
	case errors.Is(err, egressoverage.ErrNotElectable):
		api.WriteProblem(w, egressoverage.NotElectableProblem(acct.Plan))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrInternal("could not update egress overage election"))
		return
	}
	if before.On() != after.On() {
		s.audit.Emit(r.Context(), "egress_overage.changed", &acct.ID, map[string]any{
			"enabled": after.On(),
			"plan":    string(acct.Plan),
			"actor":   "self",
		})
	}
	writeJSON(w, http.StatusOK, egressoverage.View(s.billingProvider, acct.Plan, after, now))
}
