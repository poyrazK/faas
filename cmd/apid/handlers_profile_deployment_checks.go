package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) profileDeploymentTarget(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.ProfileDeploymentCheckStore, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return state.App{}, nil, false
	}
	store, ok := s.store.(state.ProfileDeploymentCheckStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("automatic profiling checks are unavailable"))
	}
	return app, store, ok
}

func (s *server) getProfileDeploymentPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileDeploymentTarget(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetProfileDeploymentPolicy(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) saveProfileDeploymentPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileDeploymentTarget(w, r, acct)
	if !ok {
		return
	}
	var req api.SaveProfileDeploymentPolicyRequest
	if err := decodeJSONSized(r, &req, api.ProfileControlMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid automatic profiling policy"))
		return
	}
	out, problem := s.saveOwnedProfileDeploymentPolicy(r.Context(), acct, app, store, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) saveOwnedProfileDeploymentPolicy(ctx context.Context, acct state.Account, app state.App, store state.ProfileDeploymentCheckStore, req api.SaveProfileDeploymentPolicyRequest) (api.ProfileDeploymentPolicy, *api.Problem) {
	if err := state.ValidateProfileDeploymentPolicy(req); err != nil {
		return api.ProfileDeploymentPolicy{}, api.ErrValidation(err.Error())
	}
	if req.Config.CanaryGate != nil && req.Config.CanaryGate.AutoRollback && app.Manifest.ExecutionMode == api.ExecutionModeService {
		return api.ProfileDeploymentPolicy{}, api.ErrValidation("automatic profiling rollback supports request-mode canaries; service recovery requires the checked handoff flow")
	}
	if req.Config.CanaryGate != nil && !acct.Plan.TrafficSplitAllowed() {
		return api.ProfileDeploymentPolicy{}, api.ErrPlanTrafficSplitNotAllowed(acct.Plan)
	}
	if req.Config.Enabled {
		limits := api.MustLimitsFor(acct.Plan)
		if !limits.Profiling.Enabled {
			return api.ProfileDeploymentPolicy{}, api.ErrPlanFeatureGated("profiling", acct.Plan)
		}
		if req.Config.Options.Metric == "cpu_per_request" && !limits.DebugTelemetryEnabled {
			return api.ProfileDeploymentPolicy{}, api.ErrPlanFeatureGated("request telemetry", acct.Plan)
		}
		if s.profileBackend == nil {
			return api.ProfileDeploymentPolicy{}, api.ErrCapacity("CPU profiling is unavailable on this installation")
		}
	}
	out, err := store.SaveProfileDeploymentPolicy(ctx, acct.ID, app.ID, req)
	if errors.Is(err, state.ErrProfileInvestigationRevision) {
		return out, api.NewProblem(http.StatusConflict, api.CodeConflict, "Policy changed", "Reload the automatic profiling policy before saving.")
	}
	if err != nil {
		return out, profileInvestigationProblem(err)
	}
	s.audit.Emit(ctx, "profile_deployment.policy_saved", &acct.ID, map[string]any{"app_id": app.ID, "revision": out.Revision, "enabled": out.Config.Enabled, "canary_gate": out.Config.CanaryGate})
	return out, nil
}

func (s *server) listProfileDeploymentChecks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileDeploymentTarget(w, r, acct)
	if !ok {
		return
	}
	rows, err := store.ListProfileDeploymentChecks(r.Context(), acct.ID, app.ID)
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	for i := range rows {
		rows[i] = profileDeploymentCheckLinks(app.Slug, rows[i])
	}
	writeJSON(w, http.StatusOK, api.ListProfileDeploymentChecksResponse{Checks: rows})
}

func (s *server) getProfileDeploymentCheck(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, store, ok := s.profileDeploymentTarget(w, r, acct)
	if !ok {
		return
	}
	if !validProfileInvestigationID(r.PathValue("id")) {
		s.notFound(w, "deployment profiling check")
		return
	}
	out, err := store.GetProfileDeploymentCheck(r.Context(), acct.ID, app.ID, r.PathValue("id"))
	if err != nil {
		s.profileInvestigationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profileDeploymentCheckLinks(app.Slug, out))
}

func profileDeploymentCheckLinks(slug string, check api.ProfileDeploymentCheck) api.ProfileDeploymentCheck {
	base := "/dashboard/apps/" + url.PathEscape(slug) + "/profiles"
	if check.InvestigationID != "" {
		check.ComparisonURL = base + "?investigation_id=" + check.InvestigationID + "#diff-flamegraph"
		return check
	}
	if check.Baseline == nil {
		return check
	}
	b, q := *check.Baseline, check.Candidate
	v := url.Values{"deployment_id": {q.DeploymentID}, "runtime": {q.Runtime}, "start": {q.Start.Format(time.RFC3339Nano)}, "end": {q.End.Format(time.RFC3339Nano)}, "baseline_id": {b.DeploymentID}, "baseline_start": {b.Start.Format(time.RFC3339Nano)}, "baseline_end": {b.End.Format(time.RFC3339Nano)}}
	check.ComparisonURL = base + "?" + v.Encode() + "#diff-flamegraph"
	return check
}
