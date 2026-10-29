package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresOverageCapability is the catalog sub-feature that releases
// compute overage (ADR-20261025094915 decision 11). It ships at maturity
// internal, so every account reads as overage off until an operator
// releases it.
const managedPostgresOverageCapability = "postgres-overage"

// managedPostgresOverageAvailable reports whether an account may run its
// databases past the compute allowance: a paid plan with an allowance, and
// the capability released for it.
func (s *server) managedPostgresOverageAvailable(account state.Account) bool {
	if api.Plan(account.Plan) == api.PlanFree {
		return false
	}
	limits, ok := api.ManagedPostgresLimitsFor(api.Plan(account.Plan))
	if !ok || limits.MonthlyComputeUnitSeconds <= 0 {
		return false
	}
	return s.capGate == nil || s.capGate.Enabled(managedPostgresOverageCapability, platformflags.Subject{AccountID: account.ID, Plan: string(account.Plan)})
}

// managedPostgresOverageOn reports whether the subject's databases run past
// the allowance now: overage is available to the account and turned on.
func (s *server) managedPostgresOverageOn(ctx context.Context, account state.Account, subject string) (bool, error) {
	if s.managedPostgres == nil || !s.managedPostgresOverageAvailable(account) {
		return false, nil
	}
	return s.managedPostgres.Overage(ctx, subject)
}

func managedPostgresOverageView(limits api.ManagedPostgresPlanLimits, enabled bool) api.ManagedPostgresOverage {
	view := api.ManagedPostgresOverage{Enabled: enabled, ComputeAllowanceUnitSeconds: limits.MonthlyComputeUnitSeconds}
	view.ComputeCeilingUnitSeconds = managedPostgresComputeCeiling(limits, enabled)
	return view
}

// managedPostgresComputeCeiling is the plan's monthly compute ceiling before
// the operator's usage cap: the allowance, or twice it with overage on.
func managedPostgresComputeCeiling(limits api.ManagedPostgresPlanLimits, overage bool) int64 {
	if overage {
		return limits.MonthlyComputeUnitSeconds * api.ManagedPostgresOverageCeilingMultiplier
	}
	return limits.MonthlyComputeUnitSeconds
}

func (s *server) getManagedPostgresOverage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	limits, ok := s.managedPostgresOverageLimits(w, acct)
	if !ok {
		return
	}
	enabled, err := s.managedPostgresOverageOn(r.Context(), acct, acct.ResourceOwnerID())
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	writeJSON(w, http.StatusOK, managedPostgresOverageView(limits, enabled))
}

func (s *server) putManagedPostgresOverage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	limits, ok := s.managedPostgresOverageLimits(w, acct)
	if !ok {
		return
	}
	var req api.ManagedPostgresOverageRequest
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	if !s.managedPostgresOverageAvailable(acct) {
		managedPostgresPlanDenied(w, acct.Plan, "compute overage needs a paid plan; the monthly compute allowance is a hard limit on this plan")
		return
	}
	subject := acct.ResourceOwnerID()
	if err := s.managedPostgres.SetOverage(r.Context(), subject, *req.Enabled); err != nil {
		managedPostgresProblem(w, err)
		return
	}
	view := managedPostgresOverageView(limits, *req.Enabled)
	s.audit.Emit(r.Context(), "managed_postgres.overage.changed", &acct.ID, map[string]any{
		"subject_id": subject, "enabled": view.Enabled, "compute_ceiling_unit_seconds": view.ComputeCeilingUnitSeconds,
	})
	writeJSON(w, http.StatusOK, view)
}

// managedPostgresOverageLimits answers for a server without managed
// PostgreSQL or a plan without a compute allowance.
func (s *server) managedPostgresOverageLimits(w http.ResponseWriter, acct state.Account) (api.ManagedPostgresPlanLimits, bool) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return api.ManagedPostgresPlanLimits{}, false
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.MonthlyComputeUnitSeconds <= 0 {
		managedPostgresPlanDenied(w, acct.Plan, "managed PostgreSQL compute is not available for this plan")
		return api.ManagedPostgresPlanLimits{}, false
	}
	return limits, true
}
