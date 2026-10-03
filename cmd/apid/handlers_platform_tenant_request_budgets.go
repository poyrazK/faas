package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func platformTenantRequestBudgetResponse(b state.PlatformTenantRequestBudget) api.PlatformTenantRequestBudgetResponse {
	out := api.PlatformTenantRequestBudgetResponse{
		TenantID: b.TenantID, Configured: b.MaxRequestsPerMinute > 0 || b.MaxRequestsPerDay > 0,
		MaxRequestsPerMinute: b.MaxRequestsPerMinute, MaxRequestsPerDay: b.MaxRequestsPerDay,
		MinuteUsed: b.MinuteUsed, DayUsed: b.DayUsed,
		MinuteResetsAt: b.MinuteResetsAt, DayResetsAt: b.DayResetsAt,
	}
	if !b.UpdatedAt.IsZero() {
		out.UpdatedAt = &b.UpdatedAt
	}
	return out
}

func (s *server) platformTenantBudgetStore(w http.ResponseWriter, r *http.Request, acct state.Account) (state.PlatformTenant, state.PlatformTenantRequestBudgetStore, bool) {
	tenants, ok := s.platformTenantStore(w, acct)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenants)
	if !ok {
		return state.PlatformTenant{}, nil, false
	}
	store, ok := s.store.(state.PlatformTenantRequestBudgetStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant request budgets are unavailable"))
	}
	return tenant, store, ok
}

func (s *server) getPlatformTenantRequestBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantBudgetStore(w, r, acct)
	if !ok {
		return
	}
	budget, err := store.GetPlatformTenantRequestBudget(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant request budget"))
		return
	}
	writeJSON(w, http.StatusOK, platformTenantRequestBudgetResponse(budget))
}

func (s *server) setPlatformTenantRequestBudget(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenant, store, ok := s.platformTenantBudgetStore(w, r, acct)
	if !ok {
		return
	}
	var req api.SetPlatformTenantRequestBudgetRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.MaxRequestsPerMinute == nil || req.MaxRequestsPerDay == nil ||
		*req.MaxRequestsPerMinute < 0 || *req.MaxRequestsPerMinute > api.MaxPlatformTenantRequestsPerMinute ||
		*req.MaxRequestsPerDay < 0 || *req.MaxRequestsPerDay > api.MaxPlatformTenantRequestsPerDay {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid customer request budget", "both ceilings are required, non-negative, and within the supported safety bounds"))
		return
	}
	prior, err := store.GetPlatformTenantRequestBudget(r.Context(), acct.ID, tenant.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not load platform tenant request budget"))
		return
	}
	budget, err := store.SetPlatformTenantRequestBudget(r.Context(), acct.ID, tenant.ID,
		*req.MaxRequestsPerMinute, *req.MaxRequestsPerDay)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not update platform tenant request budget"))
		return
	}
	if prior.MaxRequestsPerMinute != budget.MaxRequestsPerMinute || prior.MaxRequestsPerDay != budget.MaxRequestsPerDay {
		s.audit.Emit(r.Context(), "platform_tenant.request_budget_updated", &acct.ID, map[string]any{
			"tenant_id": tenant.ID, "max_requests_per_minute": budget.MaxRequestsPerMinute,
			"max_requests_per_day": budget.MaxRequestsPerDay,
		})
	}
	writeJSON(w, http.StatusOK, platformTenantRequestBudgetResponse(budget))
}
