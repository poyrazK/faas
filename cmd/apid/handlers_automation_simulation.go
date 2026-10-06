package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) simulateAutomation(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.SimulateAutomationRequest
	if err := decodeJSONSized(r, &body, api.AutomationSimulationRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationSimulationRequestMaxBytes, api.AutomationSimulationRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid simulation request"))
		}
		return
	}
	response, err := state.SimulateAutomation(r.Context(), body, account.Plan)
	if err != nil {
		writeAutomationSimulationError(w, err)
		return
	}
	if response.DefinitionValid {
		if store, ok := s.store.(state.WorkflowOutboundStore); ok {
			if err := store.ValidateWorkflowOutboundBindings(r.Context(), app.ID, body.Definition); err != nil {
				if !errors.Is(err, state.ErrAutomationInvalid) {
					writeAutomationError(w, err)
					return
				}
				response.DefinitionValid, response.Complete = false, false
				response.Issues = append(response.Issues, err.Error())
				response.Trace = []api.AutomationSimulationStep{}
			}
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func writeAutomationSimulationError(w http.ResponseWriter, err error) {
	var limit *state.AutomationSimulationLimitError
	if errors.As(err, &limit) {
		api.WriteProblem(w, api.NewProblem(http.StatusRequestEntityTooLarge, api.CodeRequestTooLarge, "Simulation limit exceeded", limit.Error()).WithLimit(limit.Limit, limit.Observed).WithDocs("https://gregale.dev/docs/plans#workflows"))
	} else if errors.Is(err, state.ErrAutomationSimulationInvalid) {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	} else {
		writeAutomationError(w, err)
	}
}
