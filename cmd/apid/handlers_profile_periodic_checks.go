package main

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listProfilePeriodicMonitors(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, _, ok := s.profileDeploymentTarget(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProfilePeriodicStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("periodic profiling is unavailable"))
		return
	}
	rows, err := store.ListProfilePeriodicMonitors(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	for i := range rows {
		rows[i] = profilePeriodicLinks(app.Slug, rows[i])
	}
	writeJSON(w, http.StatusOK, api.ListProfilePeriodicMonitorsResponse{Monitors: rows})
}
func profilePeriodicLinks(slug string, m api.ProfilePeriodicMonitor) api.ProfilePeriodicMonitor {
	for i := range m.History {
		h := &m.History[i]
		h.ComparisonURL = profileDeploymentCheckLinks(slug, api.ProfileDeploymentCheck{DeploymentID: m.DeploymentID, Baseline: &h.Baseline, Candidate: h.Candidate, InvestigationID: h.InvestigationID}).ComparisonURL
		if h.RouteCheck != nil {
			copyCheck := *h.RouteCheck
			h.RouteCheck = &copyCheck
			h.ComparisonURL = api.ProfileRouteComparisonURL(slug, h.Baseline, h.Candidate, *h.RouteCheck)
			h.RouteCheck.ComparisonURL = h.ComparisonURL
		}
	}
	return m
}
