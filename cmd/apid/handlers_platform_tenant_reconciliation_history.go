package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	platformTenantReconciliationHistoryDefaultLimit = 50
	platformTenantReconciliationHistoryMaxLimit     = 100
)

func (s *server) listPlatformTenantReconciliationReceipts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	tenantStore, ok := s.platformTenantStore(w, acct)
	if !ok {
		return
	}
	tenant, ok := s.platformTenantByPath(w, r, acct, tenantStore)
	if !ok {
		return
	}
	store, ok := s.store.(state.PlatformTenantReconciliationReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant reconciliation history is unavailable"))
		return
	}
	pageSize, pageToken, valid := platformTenantReconciliationHistoryPage(r)
	if !valid {
		api.WriteProblem(w, api.ErrValidation("page_size must be an integer from 1 to 100"))
		return
	}
	receipts, nextToken, err := store.ListPlatformTenantReconciliationReceipts(r.Context(), acct.ID, tenant.ID, pageSize, pageToken)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such platform tenant")
			return
		}
		if errors.Is(err, state.ErrInvalidArgument) {
			api.WriteProblem(w, api.ErrValidation("page_token is invalid"))
			return
		}
		api.WriteProblem(w, api.ErrInternal("could not list platform tenant reconciliation history"))
		return
	}
	out := api.PlatformTenantReconciliationReceiptListResponse{Receipts: receipts, NextPageToken: nextToken}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) getPlatformTenantReconciliationReceipt(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
		s.notFound(w, "no such platform tenant reconciliation receipt")
		return
	}
	store, ok := s.store.(state.PlatformTenantReconciliationReceiptStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("platform tenant reconciliation history is unavailable"))
		return
	}
	receipt, err := store.GetPlatformTenantReconciliationReceipt(r.Context(), acct.ID, tenant.ID, receiptID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such platform tenant reconciliation receipt")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not read platform tenant reconciliation receipt"))
		return
	}
	writeJSON(w, http.StatusOK, receipt)
}

func platformTenantReconciliationHistoryPage(r *http.Request) (pageSize int, pageToken string, valid bool) {
	pageSize = platformTenantReconciliationHistoryDefaultLimit
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > platformTenantReconciliationHistoryMaxLimit {
			return 0, "", false
		}
		pageSize = parsed
	}
	return pageSize, r.URL.Query().Get("page_token"), true
}
