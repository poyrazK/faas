package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewAccountWorkflowActions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowActionPreviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow action preview unavailable"))
		return
	}
	var req api.OperationWorkflowActionPreviewRequest
	if !decodeOperationBody(w, r, &req, 65536) {
		return
	}
	out, err := store.PreviewAccountWorkflowActions(r.Context(), acct.ID, app.ID, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) previewPlatformTenantSelfWorkflowActions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowActionPreviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow action preview unavailable"))
		return
	}
	var req api.OperationWorkflowActionPreviewRequest
	if !decodeOperationBody(w, r, &req, 65536) {
		return
	}
	out, err := store.PreviewPlatformTenantWorkflowActions(r.Context(), acct.ID, tenant, req)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
