// Package storedworkflowlive implements the live-verification HTTP surface.
package storedworkflowlive

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type Service struct {
	Store          state.StoredWorkflowLiveStore
	RuntimeEnabled bool
}

func (s Service) Start(w http.ResponseWriter, r *http.Request, appID string) {
	r.Body = http.MaxBytesReader(w, r.Body, api.StoredWorkflowLiveStartMaxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var body api.StartStoredWorkflowLiveVerificationRequest
	if err := decoder.Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowLiveStartMaxBytes, api.StoredWorkflowLiveStartMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("Enter a valid, bounded live verification request."))
		}
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.ErrValidation("Expected one live verification request."))
		return
	}
	view, replayed, err := s.Store.StartStoredWorkflowLiveVerification(r.Context(), appID, r.PathValue("name"), r.Header.Get("Idempotency-Key"), body, s.RuntimeEnabled)
	if err != nil {
		writeError(w, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(w, http.StatusCreated, view)
}

func (s Service) List(w http.ResponseWriter, r *http.Request, appID string) {
	view, err := s.Store.ListStoredWorkflowLiveVerifications(r.Context(), appID, r.PathValue("name"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s Service) Get(w http.ResponseWriter, r *http.Request, appID string) {
	view, err := s.Store.GetStoredWorkflowLiveVerification(r.Context(), appID, r.PathValue("name"), r.PathValue("run_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s Service) Check(w http.ResponseWriter, r *http.Request, appID string) {
	view, err := s.Store.CheckStoredWorkflowLiveVerification(r.Context(), appID, r.PathValue("name"), r.PathValue("run_id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, err error) {
	var problem *api.Problem
	if errors.As(err, &problem) {
		api.WriteProblem(w, problem)
		return
	}
	switch {
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrWorkflowRunNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "No such live verification or stored workflow."))
	case errors.Is(err, state.ErrStoredWorkflowVersionConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeStoredWorkflowVersionConflict, "Verification target changed", "Reload the saved case, published revision and deployment before starting verification."))
	case errors.Is(err, state.ErrWorkflowRunIdempotencyConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Idempotency key already used", "Use a new key for a different live verification request."))
	case errors.Is(err, state.ErrWorkflowDeploymentUnavailable), errors.Is(err, state.ErrWorkflowEventTargetUnavailable):
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
	case errors.Is(err, state.ErrStoredWorkflowInvalid):
		api.WriteProblem(w, api.ErrValidation("Save a valid published stored workflow, case input, and live expectations first."))
	default:
		api.WriteProblem(w, api.ErrCapacity("Live verification is unavailable; retry this request with the same idempotency key."))
	}
}
