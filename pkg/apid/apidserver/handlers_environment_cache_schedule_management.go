package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cacheschedule"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listEnvironmentCacheSchedules(w http.ResponseWriter, r *http.Request, a state.Account) {
	selected, options, err := cacheschedule.ListQuery(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid cache schedule page selector"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, selected, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheScheduleManagement)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache schedule discovery unavailable"))
		return
	}
	out, err := store.ListEnvironmentCacheSchedules(r.Context(), a.ID, app.ID, env.Slug, options)
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
	writeJSON(w, http.StatusOK, out)
}
func (s *server) rescheduleEnvironmentCache(w http.ResponseWriter, r *http.Request, a state.Account) {
	id := r.PathValue("schedule")
	var req api.RescheduleEnvironmentCacheRequest
	if decodeJSONSized(r, &req, api.RouteHealthRequestMaxBytes) != nil || api.ValidateRescheduleEnvironmentCache(id, req) != nil {
		api.WriteProblem(w, api.ErrValidation("invalid cache schedule replacement"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheScheduleManagement)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache rescheduling unavailable"))
		return
	}
	out, err := store.RescheduleEnvironmentCache(r.Context(), a.ID, app.ID, env.Slug, id, req)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "environment_cache.schedule_rescheduled", &a.ID, map[string]any{"app_id": app.ID, "environment_id": env.ID, "original_schedule_id": id, "replacement_schedule_id": out.ScheduleID, "execute_at": out.ExecuteAt})
	writeJSON(w, http.StatusOK, out)
}
