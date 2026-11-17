package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) simulateStoredWorkflow(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.SimulateStoredWorkflowRequest
	if err := decodeJSONSized(r, &body, api.StoredWorkflowSimulationRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowSimulationRequestMaxBytes, api.StoredWorkflowSimulationRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid simulation request"))
		}
		return
	}
	response, err := state.SimulateStoredWorkflow(r.Context(), body, account.Plan)
	if err != nil {
		writeStoredWorkflowSimulationError(w, err)
		return
	}
	if response.DefinitionValid {
		if store, ok := s.store.(state.WorkflowOutboundStore); ok {
			if err := store.ValidateWorkflowOutboundBindings(r.Context(), app.ID, body.Definition); err != nil {
				if !errors.Is(err, state.ErrStoredWorkflowInvalid) {
					writeStoredWorkflowError(w, err)
					return
				}
				response.DefinitionValid, response.Complete = false, false
				response.Issues = append(response.Issues, err.Error())
				response.Trace = []api.StoredWorkflowSimulationStep{}
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func writeStoredWorkflowSimulationError(w http.ResponseWriter, err error) {
	var limit *state.StoredWorkflowSimulationLimitError
	if errors.As(err, &limit) {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge, "Simulation limit exceeded", limit.Error()).WithLimit(limit.Limit, limit.Observed).WithDocs("https://gregale.dev/docs/plans#workflows"))
	} else if errors.Is(err, state.ErrStoredWorkflowSimulationInvalid) {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	} else {
		writeStoredWorkflowError(w, err)
	}
}
