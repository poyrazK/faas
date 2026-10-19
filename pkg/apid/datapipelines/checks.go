package datapipelines

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func (h *Handler) RequestCheck(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	var req api.RequestDataConnectionCheck
	if httpkit.DecodeJSONSized(r, &req, api.DataPipelineRequestMaxBytes) != nil {
		problem(w, datapipeline.ErrInvalid)
		return
	}
	check, err := h.Service.RequestCheck(r.Context(), account, r.PathValue("id"), req)
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusAccepted, check)
}

func (h *Handler) GetCheck(w http.ResponseWriter, r *http.Request, account string) {
	if !h.ready(w) {
		return
	}
	check, err := h.Service.Store.Check(r.Context(), account, r.PathValue("id"))
	if err != nil {
		problem(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, check.DataConnectionCheck)
}
