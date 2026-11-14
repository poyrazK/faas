package storedworkflowlive

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SuiteService stores references only. It cannot admit or mutate workflow runs.
type SuiteService struct {
	Store state.StoredWorkflowLiveSuiteStore
}

func (s SuiteService) Save(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	request, ok := readSuiteRequest(w, r)
	if !ok {
		return
	}
	view, err := s.Store.SaveStoredWorkflowLiveSuite(r.Context(), appID, r.PathValue("name"), r.PathValue("suite_id"), request)
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func readSuiteRequest(w http.ResponseWriter, r *http.Request) (api.SaveStoredWorkflowLiveSuiteRequest, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, api.StoredWorkflowLiveSuiteRequestMaxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request api.SaveStoredWorkflowLiveSuiteRequest
	if err := decoder.Decode(&request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowLiveSuiteRequestMaxBytes, api.StoredWorkflowLiveSuiteRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("Enter a bounded suite selection and existing verification run IDs; verdicts and payloads are not accepted."))
		}
		return request, false
	}
	if decoder.Decode(new(any)) != io.EOF {
		api.WriteProblem(w, api.ErrValidation("Expected exactly one suite record."))
		return request, false
	}
	return request, true
}

func (s SuiteService) Get(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := s.Store.GetStoredWorkflowLiveSuite(r.Context(), appID, r.PathValue("name"), r.PathValue("suite_id"))
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s SuiteService) List(w http.ResponseWriter, r *http.Request, appID string) {
	w.Header().Set("Cache-Control", "no-store")
	view, err := s.Store.ListStoredWorkflowLiveSuites(r.Context(), appID, r.PathValue("name"))
	if err != nil {
		writeSuiteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func writeSuiteError(w http.ResponseWriter, err error) {
	var problem *api.Problem
	if errors.As(err, &problem) {
		api.WriteProblem(w, problem)
		return
	}
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrWorkflowRunNotFound):
		problem = api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Suite unavailable", "No such retained suite, verification or stored workflow.")
	case errors.Is(err, state.ErrWorkflowRunIdempotencyConflict):
		problem = api.NewProblem(http.StatusConflict, api.CodeConflict, "Suite identity already used", "The suite target, ordered selection and existing run links are immutable. Use a new suite UUID for a different selection.")
	case errors.Is(err, state.ErrStoredWorkflowVersionConflict):
		problem = api.NewProblem(http.StatusConflict, api.CodeStoredWorkflowVersionConflict, "Suite target changed", "Reload the saved stored workflow version, published revision and deployment before creating a shared suite.")
	case errors.Is(err, state.ErrStoredWorkflowInvalid):
		problem = api.ErrValidation("Select valid saved live cases for the published stored workflow.")
	default:
		problem = api.ErrCapacity("Shared suite history is unavailable. Retry with the original suite UUID and selection.")
	}
	api.WriteProblem(w, problem)
}
