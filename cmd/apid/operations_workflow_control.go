// adr: 640
package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getWorkflowOperationExecutionControl(w http.ResponseWriter, r *http.Request) {
	authority, ok := s.workflowOperationRuntimeAuthority(w, r)
	if !ok {
		return
	}
	store, ok := s.store.(state.OperationWorkflowControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow operation control unavailable"))
		return
	}
	response, err := store.WorkflowOperationExecutionControl(r.Context(), r.PathValue("id"), authority)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}
