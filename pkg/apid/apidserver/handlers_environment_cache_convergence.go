package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getEnvironmentCacheConvergence(w http.ResponseWriter, r *http.Request, a state.Account) {
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheConvergenceReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment cache convergence unavailable"))
		return
	}
	out, err := store.GetEnvironmentCacheConvergence(r.Context(), a.ID, app.ID, env.Slug)
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
