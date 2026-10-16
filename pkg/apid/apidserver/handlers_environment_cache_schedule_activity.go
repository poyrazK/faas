package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/cacheschedule"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEnvironmentCacheScheduleActivity(w http.ResponseWriter, r *http.Request, a state.Account) {
	selected, after, limit, err := cacheschedule.ActivityQuery(r)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid schedule activity page selector"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, selected, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheScheduleActivityReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache schedule activity unavailable"))
		return
	}
	out, err := store.GetEnvironmentCacheScheduleActivity(r.Context(), a.ID, app.ID, env.Slug, after, limit)
	if err != nil {
		if p := cacheschedule.ActivityProblem(err); p != nil {
			if stale := s.scopedTelemetryIdentity(r.Context(), a, app, env); stale != nil {
				api.WriteProblem(w, stale)
				return
			}
			api.WriteProblem(w, p)
			return
		}
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
