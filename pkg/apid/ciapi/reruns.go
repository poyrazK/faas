package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) RerunExecution(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) || !validID(w, r.PathValue("id")) {
		return
	}
	store, ok := h.Store.(ci.ExecutionRerunStore)
	if !ok || !h.ExecutionReady || !h.Config.WorkerEnabled || !h.Config.TransportEnabled || !h.Config.DispatchAllows(acct.ID) {
		httpkit.NotFound(w, "CI execution admission is not available")
		return
	}
	var req *struct{}
	if !httpkit.DecodeJSONLimit(w, r, &req, api.CIRequestMaxBytes) {
		return
	}
	if req == nil {
		api.WriteProblem(w, api.ErrValidation("rerun requires an empty JSON object"))
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if !ci.ValidIdempotencyKey(key) {
		api.WriteProblem(w, api.ErrValidation("rerun requires a bounded printable Idempotency-Key and no configuration overrides"))
		return
	}
	row, replay, err := ci.RerunJobs(r.Context(), h.Config, store, h.VerifyGitHub, acct.ID, r.PathValue("id"), key)
	if err != nil {
		writeError(w, err)
		return
	}
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	} else {
		h.audit(r, acct.ID, "ci.execution.rerun_admitted", map[string]any{"execution_id": row.ID, "parent_execution_id": row.Rerun.ParentExecutionID, "root_execution_id": row.Rerun.RootExecutionID})
	}
	httpkit.WriteJSON(w, http.StatusAccepted, executionResponse(row))
}
