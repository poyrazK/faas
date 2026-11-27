package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/runtasks"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) rerunExecutionPipeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireExecutionAPI(w) {
		return
	}
	access, p := executionAccessForRequest(r)
	if p != nil {
		writeExecutionAccessError(w, p)
		return
	}
	store, ok := s.store.(state.ExecutionPipelineProgressStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("pipeline storage unavailable"))
		return
	}
	principal := access.principal
	if principal == nil {
		principal = &acct.ID
	}
	parent, err := store.ExecutionPipelineJob(r.Context(), acct.ID, r.PathValue("id"), principal)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	var request api.RerunExecutionPipelineRequest
	if err = decodeExecutionTaskBody(w, r, &request, api.ExecutionWorkflowManagedPlanMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid rerun body: "+err.Error()))
		return
	}
	runs, ok := s.store.(state.ExecutionStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Run receipts unavailable"))
		return
	}
	submit := s.executionPipelineSubmitter(r, acct)
	row, replayed, err := submit.RerunPipeline(r.Context(), parent, request, principal, access.broad, access.controlWide, runs)
	if err != nil {
		api.WriteProblem(w, runtasks.Problem(err))
		return
	}
	out, err := s.pipelineResponse(r.Context(), row)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("pipeline status unavailable"))
		return
	}
	if !replayed {
		s.auditManagedExecutionWorkflow(r, acct.ID, row)
	}
	writeJSON(w, http.StatusAccepted, out)
}
