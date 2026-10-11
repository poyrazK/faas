package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) automationFailurePolicyTarget(w http.ResponseWriter, r *http.Request, account state.Account) (state.App, state.AutomationFailurePolicyStore, bool) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return app, nil, false
	}
	store, ok := s.store.(state.AutomationFailurePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("automation failure policies unavailable"))
		return app, nil, false
	}
	return app, store, true
}
func (s *server) getAutomationFailurePolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, store, ok := s.automationFailurePolicyTarget(w, r, account)
	if !ok {
		return
	}
	out, err := store.GetAutomationFailurePolicy(r.Context(), app.ID, r.PathValue("name"))
	if err != nil {
		writeAutomationFailurePolicyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func (s *server) setAutomationFailurePolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, store, ok := s.automationFailurePolicyTarget(w, r, account)
	if !ok {
		return
	}
	var body api.SetAutomationFailurePolicyRequest
	if !decodeAutomationFailurePolicyBody(w, r, &body) {
		return
	}
	out, err := store.SetAutomationFailurePolicy(r.Context(), app.ID, r.PathValue("name"), body)
	if err != nil {
		writeAutomationFailurePolicyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func (s *server) resumeAutomationFailurePause(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, store, ok := s.automationFailurePolicyTarget(w, r, account)
	if !ok {
		return
	}
	var body api.ResumeAutomationFailurePauseRequest
	if !decodeAutomationFailurePolicyBody(w, r, &body) {
		return
	}
	if body.ExpectedGeneration <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_generation must be positive"))
		return
	}
	out, err := store.ResumeAutomationFailurePause(r.Context(), app.ID, r.PathValue("name"), account.ID, body)
	if err != nil {
		writeAutomationFailurePolicyError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}
func decodeAutomationFailurePolicyBody(w http.ResponseWriter, r *http.Request, body any) bool {
	if err := decodeJSONSized(r, body, api.AutomationFailurePolicyRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationFailurePolicyRequestMaxBytes, api.AutomationFailurePolicyRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid failure policy request"))
		}
		return false
	}
	return true
}
func writeAutomationFailurePolicyError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrAutomationFailurePolicyConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "automation_failure_policy_conflict", "Automation failure policy changed", "Reload the policy and pause generation before changing or resuming it."))
		return
	}
	writeAutomationError(w, err)
}
