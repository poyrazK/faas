package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) approveApplicationStandardReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	orgID, id, ok := s.applicationStandardRolloutIdentity(w, r, "review")
	if !ok {
		return
	}
	var req api.ApproveApplicationStandardReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid application standard review approval body"))
		return
	}
	store, ok := s.store.(state.ApplicationStandardOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standard approval is unavailable"))
		return
	}
	operation, err := store.ApproveApplicationStandardReview(r.Context(), orgID, acct.ID, id, req.ApprovalHash)
	if err != nil {
		writeApplicationStandardRolloutError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, applicationStandardOperationResponse(operation))
}

func (s *server) controlApplicationStandardOperation(action state.ApplicationStandardOperationAction) accountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		orgID, id, ok := s.applicationStandardRolloutIdentity(w, r, "operation")
		if !ok {
			return
		}
		var req api.ControlApplicationStandardOperationRequest
		if err := decodeJSON(r, &req); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid application standard operation control body"))
			return
		}
		store, ok := s.store.(state.ApplicationStandardOperationControlStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("application standard operator controls are unavailable"))
			return
		}
		operation, err := store.ControlApplicationStandardOperation(r.Context(), orgID, acct.ID, id, req.ExpectedUpdatedAt, action)
		if err != nil {
			writeApplicationStandardRolloutError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, applicationStandardOperationResponse(operation))
	}
}

func writeApplicationStandardRolloutError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrApplicationStandardReviewStale) || errors.Is(err, state.ErrApplicationStandardReviewExpired) || errors.Is(err, state.ErrApplicationStandardOperationStale) || errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeApplicationStandardVersionStale, "Application standards inputs changed", "Create a fresh review for changed or expired inputs, or read the current operation timestamp before retrying a control."))
		return
	}
	writeApplicationStandardMutationError(w, err)
}
