package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createManagedPostgresMaskedBranch(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if s.managedPostgres == nil {
		managedPostgresNotConfiguredProblem(w)
		return
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok || limits.DatabasesMax == 0 {
		managedPostgresPlanDenied(w, acct.Plan, "upgrade to a paid plan to create a masked branch")
		return
	}
	var req api.CreateManagedPostgresMaskedBranchRequest
	if err := decodeJSONSized(r, &req, api.ManagedPostgresMaskedPolicyMaxBytes+1024); err != nil {
		managedPostgresProblem(w, managedpostgres.ErrInvalid)
		return
	}
	source, err := s.managedPostgres.Get(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	spec := managedpostgres.MaskedBranchSpec(source.Spec)
	if !managedPostgresSpecWithinPlan(limits, spec) {
		managedPostgresPlanDenied(w, acct.Plan, "the masked branch would exceed the current plan allowance")
		return
	}
	commitBudget, answered := s.chargeAgentBudget(w, r, acct.Plan, 0, postgresStanding(spec))
	if answered {
		return
	}
	branch, err := s.managedPostgres.CreateMaskedBranch(r.Context(), acct.ID, source.ID, req)
	if err != nil {
		managedPostgresProblem(w, err)
		return
	}
	commitBudget()
	s.audit.Emit(r.Context(), "managed_postgres.database.masked_branch_requested", &acct.ID, map[string]any{
		"database_id": branch.ID, "source_database_id": source.ID,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, managedPostgresView(branch))
}
