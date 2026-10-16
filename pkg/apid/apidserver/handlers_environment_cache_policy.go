package apidserver

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) environmentCacheTarget(w http.ResponseWriter, r *http.Request, a state.Account) (state.App, state.ProjectEnvironment, state.EnvironmentCachePolicyStore, bool) {
	var empty state.ProjectEnvironment
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["environment"]) != 1 || q.Get("environment") == "" {
		api.WriteProblem(w, api.ErrValidation("select exactly one named environment"))
		return state.App{}, empty, nil, false
	}
	app, ok := s.loadApp(w, r, a, r.PathValue("slug"))
	if !ok {
		return app, empty, nil, false
	}
	app, env, _, _, problem := s.telemetryEnvironment(w, r, a, app)
	if problem != nil {
		api.WriteProblem(w, problem)
		return app, env, nil, false
	}
	if env.ID == "" {
		api.WriteProblem(w, api.ErrValidation("a registered named environment is required"))
		return app, env, nil, false
	}
	store, ok := s.store.(state.EnvironmentCachePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment cache policy unavailable"))
	}
	return app, env, store, ok
}
func (s *server) getEnvironmentCachePolicy(w http.ResponseWriter, r *http.Request, a state.Account) {
	app, env, store, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	out, err := store.GetEnvironmentCachePolicy(r.Context(), a.ID, app.ID, env.Slug)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != env.Slug {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	if problem := s.scopedTelemetryIdentity(r.Context(), a, app, env); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *server) putEnvironmentCachePolicy(w http.ResponseWriter, r *http.Request, a state.Account) {
	var req api.SetEnvironmentCachePolicyRequest
	if decodeJSONSized(r, &req, api.RouteHealthRequestMaxBytes) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid environment cache policy"))
		return
	}
	if err := api.ValidateEnvironmentCachePolicyRequest(req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	app, env, store, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	if req.AppID != app.ID || req.ProjectID != env.ProjectID || req.EnvironmentID != env.ID {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	out, err := store.SetEnvironmentCachePolicy(r.Context(), a.ID, app.ID, env.Slug, req)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "environment_cache_policy.updated", &a.ID, map[string]any{"app_id": app.ID, "environment_id": env.ID, "revision": out.Revision, "inherited": out.Inherited, "request_id": req.RequestID, "copy_from": req.CopyFrom, "restore_revision": req.RestoreRevision})
	writeJSON(w, http.StatusOK, out)
}
func (s *server) environmentCacheError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrEnvironmentCacheAccount):
		api.WriteProblem(w, api.ErrAccountSuspended())
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Not found", "No such environment cache policy"))
	case errors.Is(err, state.ErrRouteHealthRevision):
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Cache policy revision changed", "Read the current environment cache policy before planning again"))
	case errors.Is(err, state.ErrEnvironmentCacheScheduleClaimed):
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Schedule no longer pending", "Only pending schedules can be cancelled or rescheduled; inspect its status"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Request ID reused", "The request ID belongs to another cache policy proposal"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid environment cache policy"))
	default:
		api.WriteProblem(w, api.ErrCapacity("environment cache policy unavailable"))
	}
}
