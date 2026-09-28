package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) planPlatformTenantReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantReconciliationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant reconciliation is unavailable"))
		return
	}
	var req api.PlanPlatformTenantReconciliationRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	in, valid := platformTenantReconciliationInput(acct, tenant, req.Consumers, req.SurfaceIDs, req.Surfaces)
	if !valid {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid reconciliation plan", "consumer and surface references, names, and hostnames must be valid and unique"))
		return
	}
	if len(req.Surfaces) > 0 && !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotEnabled())
		return
	}
	plan, err := store.PlanPlatformTenantReconciliation(r.Context(), in)
	if err != nil {
		s.platformTenantApplyError(w, err, acct.Plan)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) applyPlatformTenantReconciliation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantReconciliationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant reconciliation is unavailable"))
		return
	}
	var req api.ApplyPlatformTenantReconciliationRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.ExpectedPlanHash == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Expected plan hash required", "preview the reconciliation and provide its plan_hash"))
		return
	}
	in, valid := platformTenantReconciliationInput(acct, tenant, req.Consumers, req.SurfaceIDs, req.Surfaces)
	if !valid {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid reconciliation plan", "consumer and surface references, names, and hostnames must be valid and unique"))
		return
	}
	if len(req.Surfaces) > 0 && !s.runtimeBool(runtimeConfigTenantSurfaces, api.TenantSurfacesEnabled()) {
		api.WriteProblem(w, api.ErrTenantSurfacesNotEnabled())
		return
	}
	result, err := store.ApplyPlatformTenantReconciliation(r.Context(), in, req.ExpectedPlanHash)
	if err != nil {
		if errors.Is(err, state.ErrPlatformTenantPlanStale) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "platform_tenant_plan_stale", "Reconciliation plan is stale",
				"tenant resources or policy changed after this plan was previewed; request a new plan before applying"))
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid plan hash", "expected_plan_hash must be the 64-character hash returned by the plan endpoint"))
			return
		}
		s.platformTenantApplyError(w, err, acct.Plan)
		return
	}
	detached, removed := 0, 0
	for _, change := range result.Changes {
		switch change.Action {
		case "detached":
			detached++
		case "removed":
			removed++
		}
	}
	if detached+removed > 0 || platformTenantReconciliationAppliedAdditions(result.Changes) {
		s.audit.Emit(r.Context(), "platform_tenant.reconciled", &acct.ID, map[string]any{
			"tenant_id": result.TenantID, "plan_hash": result.PlanHash,
			"detached_resources": detached, "removed_hostnames": removed,
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func platformTenantReconciliationInput(acct state.Account, tenant state.PlatformTenant,
	consumers []api.ApplyPlatformTenantConsumerRequest, surfaceIDs []string, surfaces []api.ApplyPlatformTenantSurfaceRequest) (state.PlatformTenantReconciliationParams, bool) {
	req := api.ApplyPlatformTenantRequest{ExternalRef: tenant.ExternalRef, Name: tenant.Name,
		Consumers: consumers, SurfaceIDs: surfaceIDs, Surfaces: surfaces}
	in, valid := platformTenantApplyInput(acct, req)
	return state.PlatformTenantReconciliationParams{TenantID: tenant.ID, ApplyPlatformTenantParams: in}, valid
}

func platformTenantReconciliationAppliedAdditions(changes []api.PlatformTenantReconciliationPlanChange) bool {
	for _, change := range changes {
		if change.Action == "created" || change.Action == "linked" {
			return true
		}
	}
	return false
}
