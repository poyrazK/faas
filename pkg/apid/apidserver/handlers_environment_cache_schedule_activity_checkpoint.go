package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEnvironmentCacheScheduleActivityCheckpoint(w http.ResponseWriter, r *http.Request, a state.Account) {
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheScheduleActivityCheckpointReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache activity checkpoints unavailable"))
		return
	}
	out, err := store.GetEnvironmentCacheScheduleActivityCheckpoint(r.Context(), a.ID, app.ID, env.Slug)
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
