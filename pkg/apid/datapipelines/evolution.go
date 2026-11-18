package datapipelines

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) Evolve(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineEvolutionRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	if err := h.Service.Evolve(r.Context(), account, r.PathValue("id"), req); err != nil {
		problem(w, err)
		return
	}
	p, err := h.Service.Store.Get(r.Context(), account, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusAccepted, p.DataPipeline)
}

func (h *Handler) ApproveSchema(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.DataPipelineSchemaApprovalRequest
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	if err := h.Service.ApproveSchema(r.Context(), account, r.PathValue("id"), req.ExpectedRevision, req.ChangeID, req.Retry); err != nil {
		problem(w, err)
		return
	}
	p, err := h.Service.Store.Get(r.Context(), account, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusAccepted, p.DataPipeline)
}
