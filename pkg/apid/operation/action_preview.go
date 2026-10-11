package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) PreviewAccountWorkflowActions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowActionPreviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow action preview unavailable"))
		return
	}
	var req api.OperationWorkflowActionPreviewRequest
	if !DecodeOperationBody(w, r, &req, 65536) {
		return
	}
	out, err := store.PreviewAccountWorkflowActions(r.Context(), acct.ID, app.ID, req)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) PreviewPlatformTenantSelfWorkflowActions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := httpkit.PlatformTenantSelfID(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowActionPreviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow action preview unavailable"))
		return
	}
	var req api.OperationWorkflowActionPreviewRequest
	if !DecodeOperationBody(w, r, &req, 65536) {
		return
	}
	out, err := store.PreviewPlatformTenantWorkflowActions(r.Context(), acct.ID, tenant, req)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
