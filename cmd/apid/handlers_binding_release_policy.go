package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) bindingReleasePolicyTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.BindingReleasePolicyStore, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = state.DefaultEnvScope
	}
	if problem := api.ValidateScope(scope); problem != nil {
		api.WriteProblem(w, problem)
		return state.App{}, nil, "", false
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, nil, scope, false
	}
	store, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("binding release policies are unavailable"))
	}
	return app, store, scope, ok
}
func (s *server) getBindingReleasePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, scope, ok := s.bindingReleasePolicyTarget(w, r, acct)
	if !ok {
		return
	}
	p, err := store.GetBindingReleasePolicy(r.Context(), acct.ID, app.ID, scope)
	if err != nil {
		s.bindingReleasePolicyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}
func (s *server) putBindingReleasePolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var request api.SetBindingReleasePolicyRequest
	if err := decodeJSONSized(r, &request, api.RoutePolicyRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid binding release policy request"))
		return
	}
	if err := api.ValidateBindingReleasePolicyRequest(request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, store, scope, ok := s.bindingReleasePolicyTarget(w, r, acct)
	if !ok {
		return
	}
	p, err := store.SetBindingReleasePolicy(r.Context(), acct.ID, app.ID, scope, request)
	if err != nil {
		s.bindingReleasePolicyError(w, err)
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "bindings.release_policy_updated", &acct.ID, map[string]any{"app_id": app.ID, "scope": scope, "mode": p.Mode, "revision": p.Revision, "reason": p.Reason})
	}
	writeJSON(w, http.StatusOK, p)
}
func (s *server) bindingReleasePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrBindingReleasePolicyRevision):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeBindingReleasePolicyChanged, "Release policy changed", "Read the current policy revision before updating it."))
	case errors.Is(err, state.ErrNotFound):
		s.notFound(w, "app")
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid binding release policy"))
	default:
		api.WriteProblem(w, api.ErrCapacity("binding release policy could not be read or updated"))
	}
}
