package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getStoredWorkflowPublishPolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.StoredWorkflowPublishCheckStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("publishing checks unavailable"))
		return
	}
	out, err := store.GetStoredWorkflowPublishPolicy(r.Context(), app.ID)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) setStoredWorkflowPublishPolicy(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.SetStoredWorkflowPublishPolicyRequest
	if !decodeStoredWorkflowBody(w, r, &body) {
		return
	}
	store, ok := s.store.(state.StoredWorkflowPublishCheckStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("publishing checks unavailable"))
		return
	}
	out, err := store.SetStoredWorkflowPublishPolicy(r.Context(), app.ID, body)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) checkStoredWorkflowPublication(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	var body api.CheckStoredWorkflowPublicationRequest
	if err := decodeJSONSized(r, &body, api.StoredWorkflowPublishCheckMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.StoredWorkflowPublishCheckMaxBytes, api.StoredWorkflowPublishCheckMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid publication check request"))
		}
		return
	}
	store, ok := s.store.(state.StoredWorkflowPublishCheckStore)
	if !ok {
		writeStoredWorkflowError(w, errors.New("publishing checks unavailable"))
		return
	}
	keyID := ""
	if _, key, authenticated := authmw.AccountFromContext(r); authenticated && key != nil {
		keyID = key.ID
	}
	out, err := store.CheckStoredWorkflowPublication(r.Context(), app.ID, r.PathValue("name"), account.ID, keyID, body, account.Plan)
	if err != nil {
		writeStoredWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
