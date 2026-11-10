package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) storedWorkflowFailurePolicyTarget(w http.ResponseWriter, r *http.Request, account state.Account) (state.App, state.StoredWorkflowFailurePolicyStore, bool) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.StoredWorkflowFailurePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("stored workflow failure policies unavailable"))
		return app, nil, false
	}
	return app, store, true
}
func (s *server) getStoredWorkflowFailurePolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, store, ok := s.storedWorkflowFailurePolicyTarget(w, r, account)
	if !ok {
		return
	}
	out, err := store.GetStoredWorkflowFailurePolicy(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeStoredWorkflowFailurePolicyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func (s *server) setStoredWorkflowFailurePolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, store, ok := s.storedWorkflowFailurePolicyTarget(w, r, account)
	if !ok {
		return
	}
	var body api.SetStoredWorkflowFailurePolicyRequest
	if !decodeStoredWorkflowFailurePolicyBody(w, r, &body) {
		return
	}
	out, err := store.SetStoredWorkflowFailurePolicy(r.Context(), app.ID, r.PathValue("name"), body)
	if err != nil {
		writeStoredWorkflowFailurePolicyError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.stored_workflow_failure_policy_changed", &account.ID, map[string]any{
		"app_id": app.ID, "stored_workflow": r.PathValue("name"), "enabled": body.Enabled != nil && *body.Enabled,
		"failure_threshold": body.FailureThreshold, "min_completed_runs": body.MinCompletedRuns, "window_seconds": body.WindowSeconds,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func (s *server) resumeStoredWorkflowFailurePause(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, store, ok := s.storedWorkflowFailurePolicyTarget(w, r, account)
	if !ok {
		return
	}
	var body api.ResumeStoredWorkflowFailurePauseRequest
	if !decodeStoredWorkflowFailurePolicyBody(w, r, &body) {
		return
	}
	if body.ExpectedGeneration <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_generation must be positive"))
		return
	}
	out, err := store.ResumeStoredWorkflowFailurePause(r.Context(), app.ID, r.PathValue("name"), account.ID, body)
	if err != nil {
		writeStoredWorkflowFailurePolicyError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.stored_workflow_failure_resumed", &account.ID, map[string]any{
		"app_id": app.ID, "stored_workflow": r.PathValue("name"), "generation": body.ExpectedGeneration,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func decodeStoredWorkflowFailurePolicyBody(w http.ResponseWriter, r *http.Request, body any) bool {
	if err := decodeJSONSized(r, body, api.StoredWorkflowFailurePolicyRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowFailurePolicyRequestMaxBytes, api.StoredWorkflowFailurePolicyRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid failure policy request"))
		}
		return false
	}
	return true
}
func writeStoredWorkflowFailurePolicyError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrStoredWorkflowFailurePolicyConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "stored_workflow_failure_policy_conflict", "Stored workflow failure policy changed", "Reload the policy and pause generation before changing or resuming it."))
		return
	}
	writeStoredWorkflowError(w, err)
}
