package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applyPlatformTenantOffboarding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantOffboardingApplyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant offboarding apply is unavailable"))
		return
	}
	var req api.ApplyPlatformTenantOffboardingRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.ExpectedPlanHash == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Expected plan hash required", "preview offboarding and provide its plan_hash"))
		return
	}
	result, err := store.ApplyPlatformTenantOffboarding(r.Context(), acct.ID, tenant.ID, req.ExpectedPlanHash)
	if err != nil {
		if errors.Is(err, state.ErrPlatformTenantPlanStale) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "platform_tenant_plan_stale", "Offboarding plan is stale",
				"tenant resources or policy changed after this plan was previewed; request a new plan before applying"))
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
				"Invalid plan hash", "expected_plan_hash must be the 64-character hash returned by the offboarding preview"))
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such platform tenant")
			return
		}
		api.WriteProblem(w, tenantBindingWriteProblem(err, api.ErrInternal("could not apply platform tenant offboarding")))
		return
	}
	s.audit.Emit(r.Context(), "platform_tenant.offboarded", &acct.ID, map[string]any{
		"tenant_id": result.TenantID, "receipt_id": result.ReceiptID, "plan_hash": result.PlanHash,
		"revoked_consumer_keys": result.Actions.RevokeConsumerKeys,
		"revoked_access_tokens": result.Actions.RevokeAccessTokens,
		"detached_consumers":    result.Actions.DetachManagedConsumers,
		"detached_surfaces":     result.Actions.DetachManagedSurfaces,
		"removed_hostnames":     result.Actions.RemoveManagedHostnames,
	})
	writeJSON(w, http.StatusOK, result)
}

func (s *server) listPlatformTenantOffboardingReceipts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantOffboardingReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant offboarding history is unavailable"))
		return
	}
	pageSize, pageToken, valid := platformTenantReconciliationHistoryPage(r)
	if !valid {
		api.WriteProblem(w, api.ErrValidation("page_size must be an integer from 1 to 100"))
		return
	}
	receipts, nextToken, err := store.ListPlatformTenantOffboardingReceipts(r.Context(), acct.ID, tenant.ID, pageSize, pageToken)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such platform tenant")
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.ErrValidation("page_token is invalid"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant offboarding history"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, api.PlatformTenantOffboardingReceiptListResponse{Receipts: receipts, NextPageToken: nextToken})
}

func (s *server) getPlatformTenantOffboardingReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	receiptID := r.PathValue("receipt_id")
	if _, err := uuid.Parse(receiptID); err != nil {
		s.notFound(w, "no such platform tenant offboarding receipt")
		return
	}
	store, ok := s.store.(state.PlatformTenantOffboardingReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant offboarding history is unavailable"))
		return
	}
	receipt, err := store.GetPlatformTenantOffboardingReceipt(r.Context(), acct.ID, tenant.ID, receiptID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant offboarding receipt")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read platform tenant offboarding receipt"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, receipt)
}
