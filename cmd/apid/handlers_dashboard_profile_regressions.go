package main

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func (s *server) dashboardCheckProfileRegression(w http.ResponseWriter, r *http.Request) {
	acct, app, store, ok := s.dashboardProfileInvestigationTarget(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !validProfileInvestigationID(id) {
		http.NotFound(w, r)
		return
	}
	req, err := profileRegressionForm(r.PostForm)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid regression thresholds or saved revision"))
		return
	}
	row, problem := s.checkOwnedProfileRegression(r.Context(), acct, app, store, id, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/profiles?investigation_id="+row.ID, http.StatusSeeOther)
}

func profileRegressionForm(v url.Values) (api.CheckProfileRegressionRequest, error) {
	revision, err := strconv.ParseInt(v.Get("expected_revision"), 10, 64)
	if err != nil {
		return api.CheckProfileRegressionRequest{}, err
	}
	o := api.DefaultProfileRegressionOptions()
	for _, route := range strings.Split(strings.ReplaceAll(v.Get("routes"), "\r\n", "\n"), "\n") {
		if route = strings.TrimSpace(route); route != "" {
			o.Routes = append(o.Routes, route)
		}
	}
	if v.Has("metric") {
		o.Metric = v.Get("metric")
	}
	for name, target := range map[string]*float64{"relative_increase_percent": &o.RelativeIncreasePercent, "absolute_increase_cpu_per_second": &o.AbsoluteIncreaseCPUPerSecond, "absolute_increase_cpu_seconds_per_request": &o.AbsoluteIncreaseCPUSecondsPerRequest, "minimum_coverage_ratio": &o.MinimumCoverageRatio} {
		if !v.Has(name) {
			continue
		}
		*target, err = strconv.ParseFloat(v.Get(name), 64)
		if err != nil {
			return api.CheckProfileRegressionRequest{}, err
		}
	}
	if v.Has("minimum_profiles") {
		o.MinimumProfiles, err = strconv.ParseInt(v.Get("minimum_profiles"), 10, 64)
	}
	if err == nil && v.Has("minimum_requests") {
		o.MinimumRequests, err = strconv.ParseInt(v.Get("minimum_requests"), 10, 64)
	}
	return api.CheckProfileRegressionRequest{ExpectedRevision: &revision, Options: &o}, err
}
