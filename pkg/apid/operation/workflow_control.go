// adr: 659
package operation

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) GetWorkflowOperationExecutionControl(w http.ResponseWriter, r *http.Request) {
	authority, ok := s.workflowOperationRuntimeAuthority(w, r)
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.OperationWorkflowControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow operation control unavailable"))
		return
	}
	response, err := store.WorkflowOperationExecutionControl(r.Context(), r.PathValue("id"), authority)
	if err != nil {
		WriteOperationError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, response)
}
