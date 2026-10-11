package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/frameworkprofile"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureProfiles(getenv func(string) string) error {
	if getenv("FAAS_PROFILING_ENABLED") != "1" {
		return nil
	}
	backend, err := profiling.NewPyroscope(getenv("FAAS_PYROSCOPE_URL"), getenv("FAAS_PYROSCOPE_TOKEN"))
	if err != nil {
		return err
	}
	s.profileBackend = backend
	s.profileQuerySlots = make(chan struct{}, api.ProfileMaxConcurrentQueries)
	return nil
}

func parseProfileQuery(values url.Values) (api.ProfileQuery, error) {
	q := api.ProfileQuery{Route: values.Get("route"), DeploymentID: values.Get("deployment_id"), Runtime: values.Get("runtime")}
	var err error
	q.Start, err = time.Parse(time.RFC3339Nano, values.Get("start"))
	if err != nil {
		return q, err
	}
	q.End, err = time.Parse(time.RFC3339Nano, values.Get("end"))
	return q, err
}

func (s *server) getAppProfiles(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	q, err := parseProfileQuery(r.URL.Query())
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("start and end must be RFC3339 timestamps"))
		return
	}
	out, problem := s.queryAppProfile(r.Context(), acct, app, q)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) compareAppProfiles(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.ProfileCompareRequest
	if err := decodeJSONSized(r, &req, api.ProfileControlMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid comparison request"))
		return
	}
	baseline, problem := s.queryAppProfile(r.Context(), acct, app, req.Baseline)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	candidate, problem := s.queryAppProfile(r.Context(), acct, app, req.Candidate)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, profiling.Compare(baseline, candidate))
}

func (s *server) profileQueryScope(ctx context.Context, acct state.Account, app state.App, q api.ProfileQuery) (string, *api.Problem) {
	dep, problem := s.profileQueryDeployment(ctx, acct, app, q)
	if problem != nil {
		return "", problem
	}
	if dep.Scope == "" {
		return api.DefaultEnvScope, nil
	}
	return dep.Scope, nil
}

func (s *server) profileQueryDeployment(ctx context.Context, acct state.Account, app state.App, q api.ProfileQuery) (state.Deployment, *api.Problem) {
	var empty state.Deployment
	limits := api.MustLimitsFor(acct.Plan).Profiling
	if !limits.Enabled {
		return empty, api.ErrPlanFeatureGated("profiling", acct.Plan)
	}
	if err := profiling.ValidateQuery(q, time.Now(), limits.RetentionDays); err != nil {
		return empty, api.ErrValidation(err.Error())
	}
	if _, err := uuid.Parse(q.DeploymentID); err != nil {
		return empty, api.ErrValidation("deployment_id must be a UUID")
	}
	if !profiling.ValidRuntime(q.Runtime) {
		return empty, api.ErrValidation("runtime must be a runtime name")
	}
	dep, err := s.store.DeploymentByID(ctx, q.DeploymentID)
	if err != nil || dep.AppID != app.ID {
		return empty, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "deployment does not belong to this app")
	}
	return dep, nil
}

func (s *server) queryAppProfile(ctx context.Context, acct state.Account, app state.App, q api.ProfileQuery) (api.ProfileResponse, *api.Problem) {
	var out api.ProfileResponse
	dep, problem := s.profileQueryDeployment(ctx, acct, app, q)
	if problem != nil {
		return out, problem
	}
	if s.profileBackend == nil {
		return out, api.ErrCapacity("CPU profiling is unavailable on this installation")
	}
	select {
	case s.profileQuerySlots <- struct{}{}:
		defer func() { <-s.profileQuerySlots }()
	default:
		return out, api.ErrCapacity("profile query capacity is busy")
	}
	ctx, cancel := context.WithTimeout(ctx, api.ProfileQueryTimeout)
	defer cancel()
	scope := dep.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	p, err := s.profileBackend.Query(ctx, app.AccountID, app.ID, scope, q)
	if err != nil {
		return out, api.ErrCapacity("profile backend unavailable")
	}
	out, err = profiling.View(p, q)
	if err != nil {
		return out, api.ErrCapacity("profile exceeds supported query bounds")
	}
	coverage := api.ProfileCoverage{}
	if backend, ok := s.profileBackend.(profiling.CoverageBackend); ok && ctx.Err() == nil {
		if recorded, err := backend.QueryCoverage(ctx, app.AccountID, app.ID, scope, q); err == nil {
			coverage = recorded
			if !out.Empty && coverage.ReceivedProfiles == 0 {
				coverage.Available = false
			}
		}
	}
	out.Coverage = &coverage
	profiling.AttachAttributionReasons(&out)
	profiling.LinkSources(&out, profileSourceProvenance(app, dep))
	s.attachProfileRouteRequests(ctx, acct, app, &out)
	profiling.AttachRouteLabelCoverage(&out)
	return out, nil
}

func profileSourceProvenance(app state.App, dep state.Deployment) profiling.SourceProvenance {
	origin := profiling.SourceProvenance{SourceURL: dep.SourceURL, CommitSHA: dep.CommitSHA, SourceRoot: dep.SourceRoot,
		Function: dep.Handler != "" || app.Type == state.AppTypeFunction}
	if dep.Kind == state.DeploymentKindImage || dep.Kind == state.DeploymentKindDockerfile {
		return origin
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil || frozen != nil && frozen.Source != nil && (frozen.Source.Kind == "dockerfile" || frozen.Source.Dockerfile != "") {
		return origin
	}
	if origin.Function {
		origin.ManagedPaths = true
		return origin
	}
	var profile frameworkprofile.Profile
	if json.Unmarshal(dep.InferredProfile, &profile) != nil || profile.Version != frameworkprofile.Version || profile.DockerfilePath != "" {
		return origin
	}
	switch profile.Framework {
	case "node", "express", "hono", "fastify", "nestjs", "python", "fastapi", "flask", "django", "go", "gin", "go-net-http":
		origin.ManagedPaths = true
	}
	return origin
}
