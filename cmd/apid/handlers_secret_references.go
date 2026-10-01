package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) secretReferenceContext(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.AppEnvironmentSecretReferenceSnapshot, state.AppEnvironmentSecretReferenceControlStore, bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return app, state.AppEnvironmentSecretReferenceSnapshot{}, nil, false
	}
	environment := r.URL.Query().Get("environment")
	if !api.ValidProjectEnvironmentSlug(environment) || api.ValidateScope(environment) != nil {
		api.WriteProblem(w, api.ErrValidation("environment must name a registered project environment"))
		return app, state.AppEnvironmentSecretReferenceSnapshot{}, nil, false
	}
	store, ok := s.store.(state.AppEnvironmentSecretReferenceControlStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("secret reference controls are unavailable"))
		return app, state.AppEnvironmentSecretReferenceSnapshot{}, nil, false
	}
	snapshot, err := store.ReadAppEnvironmentSecretReferences(r.Context(), acct.ID, app.ID, environment)
	if err != nil {
		writeSecretReferenceProblem(w, acct, err)
		return app, snapshot, store, false
	}
	return app, snapshot, store, true
}

func (s *server) listAppSecretReferences(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, snapshot, _, ok := s.secretReferenceContext(w, r, acct)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, api.AppSecretReferenceListResponse{
		EnvironmentID: snapshot.Target.EnvironmentID, Environment: snapshot.Target.Scope,
		References: snapshot.References, Count: snapshot.Count, Quota: api.MustLimitsFor(acct.Plan).EnvVarsMax,
	})
}

func (s *server) setAppSecretReference(w http.ResponseWriter, r *http.Request, acct state.Account) {
	key := r.PathValue("key")
	if problem := api.ValidateEnvKey(key); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, snapshot, store, ok := s.secretReferenceContext(w, r, acct)
	if !ok {
		return
	}
	var request api.PutAppSecretReferenceRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	if problem := request.Validate(); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err := store.SetAppEnvironmentSecretReference(r.Context(), snapshot.Target, key, request.Reference); err != nil {
		writeSecretReferenceProblem(w, acct, err)
		return
	}
	s.secretReferenceChanged(r, acct, app, snapshot.Target, key, "set")
	writeJSON(w, http.StatusOK, api.AppSecretReferenceResponse{EnvironmentID: snapshot.Target.EnvironmentID,
		Environment: snapshot.Target.Scope, Key: key, Reference: request.Reference})
}

func (s *server) deleteAppSecretReference(w http.ResponseWriter, r *http.Request, acct state.Account) {
	key := r.PathValue("key")
	if problem := api.ValidateEnvKey(key); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, snapshot, store, ok := s.secretReferenceContext(w, r, acct)
	if !ok {
		return
	}
	if err := store.RemoveAppEnvironmentSecretReference(r.Context(), snapshot.Target, key); err != nil {
		writeSecretReferenceProblem(w, acct, err)
		return
	}
	s.secretReferenceChanged(r, acct, app, snapshot.Target, key, "delete")
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) secretReferenceChanged(r *http.Request, acct state.Account, app state.App, target state.AppEnvironmentSecretReferenceTarget, key, operation string) {
	s.audit.Emit(r.Context(), "secret_reference."+operation, &acct.ID, map[string]any{
		"app_id": app.ID, "environment_id": target.EnvironmentID, "environment": target.Scope, "name": key,
	})
	s.notifyRuntimeConfigChange(r.Context(), db.NotifyAppEnvChanged, acct, app, operation, target.Scope, key)
}

func writeSecretReferenceProblem(w http.ResponseWriter, acct state.Account, err error) {
	if writeEnvironmentGitOpsOwnershipProblem(w, err) {
		return
	}
	switch {
	case errors.Is(err, state.ErrEnvironmentSecretReferenceSourceNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "secret_not_found", "Secret unavailable", "Create the named secret in this environment before referencing it."))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment not found", "The application or its registered project environment was not found."))
	case errors.Is(err, state.ErrQuotaExceeded):
		limits := api.MustLimitsFor(acct.Plan)
		observed := limits.EnvVarsMax + 1
		var quota *state.EnvironmentSecretReferenceQuotaError
		if errors.As(err, &quota) {
			observed = quota.Observed
		}
		api.WriteProblem(w, api.ErrPlanLimitEnvVars(limits, observed))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "secret_reference_conflict", "Reference conflict", "Reload the environment and remove any plaintext variable with this destination key before trying again."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid environment secret reference"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not access environment secret references"))
	}
}
