package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// getExecutionWorkflow returns an aggregate over only the runs visible to the
// authenticated principal. Runs-only credentials are always constrained to
// their stable key-family identity in the store query.
func (s *server) getExecutionWorkflow(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.requireExecutionAPI(w) {
		return
	}
	access, problem := executionAccessForRequest(r)
	if problem != nil {
		writeExecutionAccessError(w, problem)
		return
	}
	workflowID := r.PathValue("workflow_id")
	if err := api.ValidateExecutionWorkflowMetadata(workflowID, ""); err != nil || workflowID == "" {
		api.WriteProblem(w, api.ErrValidation("workflow_id is invalid"))
		return
	}
	workflowStore, ok := s.store.(state.ExecutionWorkflowStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("this store cannot enforce workflow ownership filters"))
		return
	}
	var principalID *string
	if !access.broad {
		principalID = access.principal
	}
	summary, err := workflowStore.ExecutionWorkflowSummary(r.Context(), acct.ID, workflowID, principalID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such workflow")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not read execution workflow"))
		return
	}
	writeJSON(w, http.StatusOK, api.ExecutionWorkflowResponse{
		WorkflowID:   workflowID,
		RunCount:     summary.RunCount,
		StatusCounts: summary.StatusCounts,
		Usage:        summary.Usage,
	})
}
