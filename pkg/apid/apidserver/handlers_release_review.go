package apidserver

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getAppReleaseReview(w http.ResponseWriter, r *http.Request, account state.Account) {
	app, ok := s.loadApp(w, r, account, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.ReleaseReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("Release review is unavailable"))
		return
	}
	_, environment, problem := s.appEnvironmentSettings(w, r, account, app, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	scope := api.DefaultEnvScope
	if environment.ID != "" {
		scope = environment.Slug
	}
	review, err := store.ReadReleaseReview(r.Context(), app.ID, scope)
	if err != nil {
		s.writeDeploymentCreateError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, review)
}

func validateReviewedStage(req api.CreateDeploymentRequest) *api.Problem {
	review := req.ExpectedReview
	if review == nil {
		if req.NativeRollout != "" {
			return api.ErrValidation("native_rollout requires expected_review")
		}
		return nil
	}
	scope := review.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	validScope := scope == api.DefaultEnvScope && req.Scope == api.DefaultEnvScope && req.Environment == "" && review.EnvironmentID == ""
	if scope != api.DefaultEnvScope {
		validScope = api.ValidProjectEnvironmentSlug(scope) && req.Environment == scope && req.Scope == "" && review.EnvironmentID != "" && review.ProjectID != "" && review.WorkloadRevision >= 0
	}
	trafficValid := req.TrafficPercent != nil && *req.TrafficPercent == 0
	if req.NativeRollout != "" {
		trafficValid = req.TrafficPercent == nil && (req.NativeRollout == api.ExecutionModeService || req.NativeRollout == api.ExecutionModeWorker) && !review.Protected && req.Canary == nil && req.RollbackOn5xx == nil && req.DisableStartupCPUBoost == nil && req.Overrides == nil && len(req.Workflows) == 0 && len(req.Sidecars) == 0 && len(req.Companions) == 0
	}
	if review.AppID == "" || len(review.ConfigurationHash) != 64 || !validScope || !trafficValid || len(review.ServingDeploymentIDs) > 1 {
		return api.NewProblem(400, api.CodeValidation, "Invalid release review", "Reviewed candidates must name the reviewed environment; native rollouts require service or worker mode, omitted traffic and an unprotected target")
	}
	return nil
}

func (s *server) reviewedEnvironmentApp(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, req api.CreateDeploymentRequest) (state.App, *state.ProjectEnvironmentWorkloadSettings, *api.Problem) {
	if req.ExpectedReview == nil || req.ExpectedReview.EnvironmentID == "" {
		return app, nil, nil
	}
	request := r.Clone(r.Context())
	copyURL := *r.URL
	query := url.Values{"environment": []string{req.Environment}}
	copyURL.RawQuery = query.Encode()
	request.URL = &copyURL
	resolved, env, problem := s.appEnvironmentSettings(w, request, account, app, false)
	if problem != nil {
		return app, nil, problem
	}
	if env.ID != req.ExpectedReview.EnvironmentID || app.ProjectID != req.ExpectedReview.ProjectID {
		return app, nil, api.NewProblem(409, api.CodeReleaseReviewChanged, "Release review changed", "Environment identity changed; create a new plan")
	}
	settings, err := state.WorkloadSettingsFromApp(resolved)
	if err != nil {
		return app, nil, api.ErrCapacity("could not resolve reviewed workload settings")
	}
	return resolved, &settings, nil
}

func writeReleaseReviewError(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, state.ErrReleaseReviewChanged) {
		return false
	}
	api.WriteProblem(w, api.NewProblem(409, api.CodeReleaseReviewChanged, "Release review changed", "App configuration or release changed; inspect it and create a new plan"))
	return true
}
