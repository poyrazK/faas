package apidserver

import (
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) createEnvironmentCachePurge(w http.ResponseWriter, r *http.Request, a state.Account) {
	var body struct {
		api.CreateEnvironmentCachePurgeRequest
		Path *string `json:"path"`
	}
	if decodeJSONSized(r, &body, api.RouteHealthRequestMaxBytes) != nil || body.Path == nil {
		api.WriteProblem(w, api.ErrValidation("an explicit purge path is required; use empty string for the whole environment"))
		return
	}
	req := body.CreateEnvironmentCachePurgeRequest
	req.Path = *body.Path
	if api.ValidateEnvironmentCachePurgeRequest(req) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid environment cache purge; select an exact path or empty path for the whole environment"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	if req.AppID != app.ID || req.ProjectID != env.ProjectID || req.EnvironmentID != env.ID {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	store, ok := s.store.(state.EnvironmentCachePurgeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment purge unavailable"))
		return
	}
	out, err := store.CreateEnvironmentCachePurge(r.Context(), a.ID, app.ID, env.Slug, req)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "environment_cache.purge_requested", &a.ID, map[string]any{"app_id": app.ID, "environment_id": env.ID, "request_id": req.RequestID, "generation": out.Generation})
	writeJSON(w, http.StatusOK, out)
}
func (s *server) getEnvironmentCachePurge(w http.ResponseWriter, r *http.Request, a state.Account) {
	request := r.PathValue("request")
	if _, err := uuid.Parse(request); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid purge request UUID"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCachePurgeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment purge unavailable"))
		return
	}
	out, err := store.GetEnvironmentCachePurge(r.Context(), a.ID, app.ID, env.Slug, request)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != env.Slug || out.RequestID != request {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	if problem := s.scopedTelemetryIdentity(r.Context(), a, app, env); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
