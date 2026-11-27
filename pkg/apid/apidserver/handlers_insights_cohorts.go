package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/insights"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
)

// Analytics cohorts under /v1/projects/{slug}/environments/{environment}/cohorts
// (ADR 20261119102741 §3). Flag rules target them with gregale.cohort.

func (s *server) cohortScope(w http.ResponseWriter, r *http.Request, acct state.Account) (insightsScope, state.AnalyticsCohortStore, bool) {
	sc, ok := s.insightsScope(w, r, acct)
	if !ok {
		return sc, nil, false
	}
	store, ok := s.store.(state.AnalyticsCohortStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("analytics cohorts unavailable"))
		return sc, nil, false
	}
	return sc, store, true
}

func cohortDefinition(req api.InsightCohortRequest) state.AnalyticsCohortDefinition {
	d := state.AnalyticsCohortDefinition{Kind: req.Kind, Event: req.Event, Where: req.Where, WithinDays: req.WithinDays,
		MinCount: req.MinCount, Window: req.Window}
	for _, st := range req.Steps {
		d.Steps = append(d.Steps, metricStep(st))
	}
	return d
}

func cohortResponse(c state.AnalyticsCohort, plan api.Plan) api.InsightCohort {
	d := c.Definition
	out := api.InsightCohort{
		InsightCohortRequest: api.InsightCohortRequest{Key: c.Key, Kind: d.Kind, Event: d.Event, Where: d.Where,
			WithinDays: d.WithinDays, MinCount: d.MinCount, Window: d.Window},
		Members: c.Members, Truncated: c.Truncated, MaxMembers: plan.AnalyticsCohortMaxMembers(), RefreshedAt: c.RefreshedAt,
		RefreshRequestedAt: c.RefreshRequestedAt, RefreshError: c.RefreshError, CreatedBy: c.CreatedBy, CreatedAt: c.CreatedAt,
	}
	for _, st := range d.Steps {
		out.Steps = append(out.Steps, apiStep(st))
	}
	return out
}

func writeCohortError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Cohort not found", "no cohort has that key in this environment"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Cohort exists", "a cohort with that key already exists"))
	case errors.Is(err, state.ErrAnalyticsCohortLimit):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "analytics_cohort_limit", "Cohort limit reached",
			"the environment already holds the maximum number of cohorts; delete one first").
			WithLimit(api.AnalyticsCohortsMaxPerEnvironment, api.AnalyticsCohortsMaxPerEnvironment))
	case errors.Is(err, insights.ErrInvalid):
		writeInsightsError(w, err)
	default:
		api.WriteProblem(w, api.ErrInternal("analytics cohorts unavailable"))
	}
}

func (s *server) listInsightCohorts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.cohortScope(w, r, acct)
	if !ok {
		return
	}
	list, err := store.ListAnalyticsCohorts(r.Context(), sc.env.ID)
	if err != nil {
		writeCohortError(w, err)
		return
	}
	out := api.InsightCohortList{Cohorts: make([]api.InsightCohort, 0, len(list))}
	for _, c := range list {
		out.Cohorts = append(out.Cohorts, cohortResponse(c, api.Plan(acct.Plan)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createInsightCohort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.cohortScope(w, r, acct)
	if !ok {
		return
	}
	var req api.InsightCohortRequest
	if !decodeJSONLimit(w, r, &req, 16<<10) {
		return
	}
	def := cohortDefinition(req)
	if def.Kind == state.CohortDid && def.MinCount == 0 {
		def.MinCount = 1
	}
	if err := insights.ValidateCohort(req.Key, def, api.Plan(acct.Plan).AnalyticsRetentionDays()); err != nil {
		writeCohortError(w, err)
		return
	}
	c, err := store.CreateAnalyticsCohort(r.Context(), state.AnalyticsCohort{AccountID: acct.ID, EnvironmentID: sc.env.ID, Key: req.Key,
		Definition: def, CreatedBy: acct.ID}, api.AnalyticsCohortsMaxPerEnvironment)
	if err != nil {
		writeCohortError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "insights.cohort_created", &acct.ID, map[string]any{"environment_id": sc.env.ID, "cohort": c.Key, "kind": def.Kind})
	writeJSON(w, http.StatusCreated, cohortResponse(c, api.Plan(acct.Plan)))
}

func (s *server) getInsightCohort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.cohortScope(w, r, acct)
	if !ok {
		return
	}
	c, err := store.GetAnalyticsCohort(r.Context(), sc.env.ID, r.PathValue("key"))
	if err != nil {
		writeCohortError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cohortResponse(c, api.Plan(acct.Plan)))
}

func (s *server) deleteInsightCohort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.cohortScope(w, r, acct)
	if !ok {
		return
	}
	key := r.PathValue("key")
	if !flags.ValidKey(key) {
		writeCohortError(w, state.ErrNotFound)
		return
	}
	deleted, err := store.DeleteAnalyticsCohort(r.Context(), sc.env.ID, key)
	if err != nil {
		writeCohortError(w, err)
		return
	}
	if !deleted {
		writeCohortError(w, state.ErrNotFound)
		return
	}
	s.audit.Emit(r.Context(), "insights.cohort_deleted", &acct.ID, map[string]any{"environment_id": sc.env.ID, "cohort": key})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) refreshInsightCohort(w http.ResponseWriter, r *http.Request, acct state.Account) {
	sc, store, ok := s.cohortScope(w, r, acct)
	if !ok {
		return
	}
	c, err := store.RequestAnalyticsCohortRefresh(r.Context(), sc.env.ID, r.PathValue("key"), time.Now())
	if err != nil {
		writeCohortError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, cohortResponse(c, api.Plan(acct.Plan)))
}
