package apidserver

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createEnvironmentCacheSchedule(w http.ResponseWriter, r *http.Request, a state.Account) {
	var body struct {
		api.CreateEnvironmentCacheScheduleRequest
		Purge *struct {
			api.CreateEnvironmentCachePurgeRequest
			Path *string `json:"path"`
		} `json:"purge"`
	}
	if decodeJSONSized(r, &body, api.RouteHealthRequestMaxBytes) != nil || body.Purge != nil && body.Purge.Path == nil {
		api.WriteProblem(w, api.ErrValidation("purge path must be explicit"))
		return
	}
	req := body.CreateEnvironmentCacheScheduleRequest
	if body.Purge != nil {
		p := body.Purge.CreateEnvironmentCachePurgeRequest
		p.Path = *body.Purge.Path
		req.Purge = &p
	}
	if api.ValidateEnvironmentCacheSchedule(req) != nil {
		api.WriteProblem(w, api.ErrValidation("select a future execution time and one exact cache action"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache scheduling unavailable"))
		return
	}
	out, err := store.CreateEnvironmentCacheSchedule(r.Context(), a.ID, app.ID, env.Slug, req)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "environment_cache.schedule_created", &a.ID, map[string]any{"app_id": app.ID, "environment_id": env.ID, "schedule_id": out.ScheduleID, "execute_at": out.ExecuteAt})
	writeJSON(w, http.StatusOK, out)
}
func (s *server) getEnvironmentCacheSchedule(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.inspectEnvironmentCacheSchedule(w, r, a, false)
}
func (s *server) cancelEnvironmentCacheSchedule(w http.ResponseWriter, r *http.Request, a state.Account) {
	s.inspectEnvironmentCacheSchedule(w, r, a, true)
}
func (s *server) inspectEnvironmentCacheSchedule(w http.ResponseWriter, r *http.Request, a state.Account, cancel bool) {
	id := r.PathValue("schedule")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid schedule UUID"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheScheduleStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache scheduling unavailable"))
		return
	}
	var out api.EnvironmentCacheSchedule
	var err error
	if cancel {
		out, err = store.CancelEnvironmentCacheSchedule(r.Context(), a.ID, app.ID, env.Slug, id)
	} else {
		out, err = store.GetEnvironmentCacheSchedule(r.Context(), a.ID, app.ID, env.Slug, id)
	}
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != env.Slug {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	if p := s.scopedTelemetryIdentity(r.Context(), a, app, env); p != nil {
		api.WriteProblem(w, p)
		return
	}
	if cancel {
		s.audit.Emit(r.Context(), "environment_cache.schedule_cancelled", &a.ID, map[string]any{"app_id": app.ID, "environment_id": env.ID, "schedule_id": id})
	}
	writeJSON(w, http.StatusOK, out)
}
