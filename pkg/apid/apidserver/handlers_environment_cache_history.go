package apidserver

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func cacheHistoryQuery(r *http.Request) (*http.Request, int64, int, bool) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return r, 0, 0, false
	}
	for key, values := range q {
		if len(values) != 1 || key != "environment" && key != "before_revision" && key != "limit" {
			return r, 0, 0, false
		}
	}
	before := int64(0)
	limit := api.EnvironmentCacheHistoryDefaultLimit
	if values, ok := q["before_revision"]; ok {
		before, err = strconv.ParseInt(values[0], 10, 64)
		if err != nil || before < 0 || before > api.RouteRequirementsMaxRevision {
			return r, 0, 0, false
		}
	}
	if values, ok := q["limit"]; ok {
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > api.EnvironmentCacheHistoryMaxLimit {
			return r, 0, 0, false
		}
	}
	clone := r.Clone(r.Context())
	u := *r.URL
	clone.URL = &u
	q.Del("limit")
	q.Del("before_revision")
	clone.URL.RawQuery = q.Encode()
	return clone, before, limit, true
}
func (s *server) listEnvironmentCacheHistory(w http.ResponseWriter, r *http.Request, a state.Account) {
	selected, before, limit, ok := cacheHistoryQuery(r)
	if !ok {
		api.WriteProblem(w, api.ErrValidation("invalid cache history page selector"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, selected, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheHistoryReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache history unavailable"))
		return
	}
	out, err := store.ListEnvironmentCachePolicyHistory(r.Context(), a.ID, app.ID, env.Slug, before, limit)
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
func (s *server) getEnvironmentCacheRevision(w http.ResponseWriter, r *http.Request, a state.Account) {
	revision, err := strconv.ParseInt(r.PathValue("revision"), 10, 64)
	if err != nil || revision < 0 || revision > api.RouteRequirementsMaxRevision {
		api.WriteProblem(w, api.ErrValidation("invalid cache history revision"))
		return
	}
	app, env, _, ok := s.environmentCacheTarget(w, r, a)
	if !ok {
		return
	}
	store, ok := s.store.(state.EnvironmentCacheHistoryReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("cache history unavailable"))
		return
	}
	out, err := store.GetEnvironmentCachePolicyRevision(r.Context(), a.ID, app.ID, env.Slug, revision)
	if err != nil {
		s.environmentCacheError(w, err)
		return
	}
	if out.AppID != app.ID || out.ProjectID != env.ProjectID || out.EnvironmentID != env.ID || out.Scope != env.Slug || out.Revision != revision {
		api.WriteProblem(w, api.NewProblem(409, api.CodeConflict, "Environment changed", "Inspect again"))
		return
	}
	if problem := s.scopedTelemetryIdentity(r.Context(), a, app, env); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
