package main

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

type profilePageData struct {
	RouteView      *dashboard.ProfileRouteView
	CanaryFinding  *profileCanaryFinding
	AppSlug        string
	Deployments    []state.Deployment
	Query          api.ProfileQuery
	Baseline       api.ProfileQuery
	Profile        *api.ProfileResponse
	Compare        *api.ProfileCompareResponse
	Error          string
	CPUChart       *dashboard.ProfileCPUChart
	Investigations *dashboard.ProfileInvestigationsView
	Automatic      *dashboard.ProfileDeploymentChecksView
}

func parseAppProfilesPath(rest string) (string, bool) {
	rest = strings.TrimSuffix(rest, "/")
	if !strings.HasSuffix(rest, "/profiles") {
		return "", false
	}
	slug := strings.TrimSuffix(rest, "/profiles")
	return slug, validSlug(slug) && !strings.Contains(slug, "/")
}

func (s *server) renderAppProfiles(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	r, finding, ok := s.canaryFindingRequest(w, r, acct, app)
	if !ok {
		return
	}
	data, savedRequest, ok := s.savedProfilePage(w, r, acct, app)
	if !ok {
		return
	}
	data.CanaryFinding = finding
	data.RouteView = dashboard.BuildProfileRoutes(app.Slug, data.Query, data.Baseline, data.Profile, data.Compare)
	data.Automatic = s.profileDeploymentChecksView(w, r, acct, app)
	if s.profileBackend != nil && api.MustLimitsFor(acct.Plan).Profiling.Enabled && data.Query.DeploymentID != "" {
		if _, problem := s.profileQueryScope(r.Context(), acct, app, data.Query); problem == nil {
			data.CPUChart = s.profileCPUChart(savedRequest, acct, app, data.Query)
		}
	}
	view, _ := AccountFrom(r.Context())
	count, _ := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), dashboard.Page{Title: slug + " CPU profiles", Body: "app_profiles", Account: dashboardAccountView(view, count), Data: data}); err != nil {
		renderProblem(w, log, err)
	}
}

func (s *server) profileCPUChart(r *http.Request, acct state.Account, app state.App, selected api.ProfileQuery) *dashboard.ProfileCPUChart {
	q := selected
	values := r.URL.Query()
	if values.Has("chart_start") || values.Has("chart_end") {
		var err error
		q.Start, err = time.Parse(time.RFC3339Nano, values.Get("chart_start"))
		if err == nil {
			q.End, err = time.Parse(time.RFC3339Nano, values.Get("chart_end"))
		}
		if err != nil {
			return &dashboard.ProfileCPUChart{Error: "Enter valid CPU chart timestamps."}
		}
	}
	if err := profiling.ValidateQuery(q, time.Now(), api.MustLimitsFor(acct.Plan).Profiling.RetentionDays); err != nil {
		return &dashboard.ProfileCPUChart{Error: "Select a CPU chart window within your plan's profile history."}
	}
	step := dashboard.ProfileChartStep(q.End.Sub(q.Start))
	series, err := s.promqlClient.QueryRange(r.Context(), dashboard.ProfileCPUQuery(app.ID, step), q.Start.Format(time.RFC3339Nano), q.End.Format(time.RFC3339Nano), step.String())
	if err != nil || len(series) != 1 {
		return &dashboard.ProfileCPUChart{Error: "CPU measurements are unavailable. You can still load profiles using the selected timestamps."}
	}
	chart := dashboard.BuildProfileCPUChart(app.Slug, selected, q, series[0].Values)
	return &chart
}

func (s *server) profilePageData(r *http.Request, acct state.Account, app state.App) profilePageData {
	data := profilePageData{AppSlug: app.Slug}
	data.Deployments, _ = s.store.ListDeploymentsForApp(r.Context(), app.ID, api.ProfileMaxDeploymentChoices, 0)
	values := r.URL.Query()
	if values.Get("deployment_id") == "" {
		data.Query = api.ProfileQuery{Runtime: app.Runtime, Start: time.Now().Add(-time.Hour), End: time.Now()}
		if data.Query.Runtime == "" {
			data.Query.Runtime = "custom"
		}
		if len(data.Deployments) > 0 {
			data.Query.DeploymentID = data.Deployments[0].ID
		}
		return data
	}
	q, err := parseProfileQuery(values)
	data.Query = q
	if err != nil {
		data.Error = "Enter RFC3339 start and end timestamps."
		return data
	}
	out, problem := s.queryAppProfile(r.Context(), acct, app, q)
	if problem != nil {
		data.Error = problem.Detail
		return data
	}
	data.Profile = &out
	if values.Get("baseline_id") == "" {
		return data
	}
	baselineValues := make(url.Values)
	for key, vals := range values {
		baselineValues[key] = append([]string(nil), vals...)
	}
	baselineValues.Set("deployment_id", values.Get("baseline_id"))
	baselineValues.Set("start", values.Get("baseline_start"))
	baselineValues.Set("end", values.Get("baseline_end"))
	baseline, err := parseProfileQuery(baselineValues)
	data.Baseline = baseline
	if err != nil {
		data.Error = "Enter RFC3339 baseline timestamps."
		return data
	}
	b, problem := s.queryAppProfile(r.Context(), acct, app, baseline)
	if problem != nil {
		data.Error = problem.Detail
		return data
	}
	compare := profiling.Compare(b, out)
	data.Compare = &compare
	return data
}
