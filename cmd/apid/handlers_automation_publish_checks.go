package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getAutomationPublishPolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.AutomationPublishCheckStore)
	if !ok {
		writeAutomationError(w, errors.New("publishing checks unavailable"))
		return
	}
	out, err := store.GetAutomationPublishPolicy(r.Context(), app.ID)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) setAutomationPublishPolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.SetAutomationPublishPolicyRequest
	if !decodeAutomationBody(w, r, &body) {
		return
	}
	store, ok := s.store.(state.AutomationPublishCheckStore)
	if !ok {
		writeAutomationError(w, errors.New("publishing checks unavailable"))
		return
	}
	out, err := store.SetAutomationPublishPolicy(r.Context(), app.ID, body)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) checkAutomationPublication(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.CheckAutomationPublicationRequest
	if err := decodeJSONSized(r, &body, api.AutomationPublishCheckMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.AutomationPublishCheckMaxBytes, api.AutomationPublishCheckMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid publication check request"))
		}
		return
	}
	store, ok := s.store.(state.AutomationPublishCheckStore)
	if !ok {
		writeAutomationError(w, errors.New("publishing checks unavailable"))
		return
	}
	keyID := ""
	if _, key, authenticated := authmw.AccountFromContext(r); authenticated && key != nil {
		keyID = key.ID
	}
	out, err := store.CheckAutomationPublication(r.Context(), app.ID, r.PathValue("name"), account.ID, keyID, body, account.Plan)
	if err != nil {
		writeAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
