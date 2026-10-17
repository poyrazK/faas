package automationlive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type SuiteExecutionService struct {
	Store          state.AutomationLiveSuiteExecutionStore
	RuntimeEnabled bool
}

func (s SuiteExecutionService) Start(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	var request api.StartAutomationLiveSuiteRequest
	if !readSuiteExecutionRequest(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	view, err := s.Store.StartAutomationLiveSuite(ctx, appID, r.PathValue("name"), r.PathValue("suite_id"), request, s.RuntimeEnabled)
	if err != nil {
		writeSuiteExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s SuiteExecutionService) Advance(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	var request api.AdvanceAutomationLiveSuiteRequest
	if !readSuiteExecutionRequest(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AutomationLiveProgressRequestTimeout)
	defer cancel()
	view, err := s.Store.AdvanceAutomationLiveSuite(ctx, appID, r.PathValue("name"), r.PathValue("suite_id"), request, s.RuntimeEnabled)
	if err != nil {
		writeSuiteExecutionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func readSuiteExecutionRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, api.AutomationLiveSuiteRequestMaxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationLiveSuiteRequestMaxBytes, api.AutomationLiveSuiteRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("Enter a valid suite request and explicit real-action confirmation."))
		}
		return false
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.ErrValidation("Expected exactly one suite execution request."))
		return false
	}
	return true
}

func writeSuiteExecutionError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrWorkflowDeploymentUnavailable) || errors.Is(err, state.ErrWorkflowEventTargetUnavailable) {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}
	writeSuiteError(w, err)
}
