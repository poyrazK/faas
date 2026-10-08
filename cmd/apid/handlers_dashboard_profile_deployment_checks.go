package main

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const profileDeploymentPolicyAction = "profile_deployment_policy"
const profileDeploymentPolicyCookie = "faas_csrf_profile_deployment_policy"

func (s *server) profileDeploymentChecksView(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App) *dashboard.ProfileDeploymentChecksView {
	view := &dashboard.ProfileDeploymentChecksView{Policy: state.DefaultProfileDeploymentPolicy(app.ID, app.Runtime)}
	store, ok := s.store.(state.ProfileDeploymentCheckStore)
	if !ok {
		view.Error = "Automatic profiling checks are unavailable."
		return view
	}
	p, err := store.GetProfileDeploymentPolicy(r.Context(), acct.ID, app.ID)
	if err != nil {
		view.Error = "The automatic check policy could not be read."
		return view
	}
	view.Policy = p
	rows, err := store.ListProfileDeploymentChecks(r.Context(), acct.ID, app.ID)
	if err != nil {
		view.Error = "Recent automatic checks could not be listed."
	}
	for _, row := range rows {
		view.Checks = append(view.Checks, profileDeploymentCheckLinks(app.Slug, row))
	}
	s.loadPeriodicMonitorHistory(r.Context(), acct.ID, app, view)
	if s.sessions == nil {
		return view
	}
	token, err := middleware.IssueForAuthenticatedNamed(s.sessions, profileDeploymentPolicyAction, acct.ID, profileDeploymentPolicyCookie)
	if err != nil {
		view.Error = "Policy editing is temporarily unavailable."
		return view
	}
	view.CanEdit, view.CSRF = true, token
	http.SetCookie(w, &http.Cookie{Name: profileDeploymentPolicyCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.domain != "", SameSite: http.SameSiteLaxMode, MaxAge: int(middleware.DefaultCSRFTTL.Seconds())})
	return view
}

func (s *server) dashboardSaveProfileDeploymentPolicy(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, api.ProfileControlMaxBytes)
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid automatic profiling policy form"))
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, profileDeploymentPolicyAction, acct.ID, profileDeploymentPolicyCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid CSRF token; reload before saving the policy"))
		return
	}
	app, store, ok := s.profileDeploymentTarget(w, r, acct)
	if !ok {
		return
	}
	req, err := profileDeploymentPolicyForm(r.PostForm)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid policy revision or capture settings"))
		return
	}
	if _, problem := s.saveOwnedProfileDeploymentPolicy(r.Context(), acct, app, store, req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	http.Redirect(w, r, "/dashboard/apps/"+url.PathEscape(app.Slug)+"/profiles#automatic-profile-checks", http.StatusSeeOther)
}

func profileDeploymentPolicyForm(v url.Values) (api.SaveProfileDeploymentPolicyRequest, error) {
	req, err := profileRegressionForm(v)
	if err != nil {
		return api.SaveProfileDeploymentPolicyRequest{}, err
	}
	window, err := strconv.Atoi(v.Get("window_seconds"))
	if err != nil {
		return api.SaveProfileDeploymentPolicyRequest{}, err
	}
	warmup, err := strconv.Atoi(v.Get("warmup_seconds"))
	if err != nil {
		return api.SaveProfileDeploymentPolicyRequest{}, err
	}
	var periodic *api.PeriodicProfilePolicy
	if v.Get("enabled") == "on" && v.Get("periodic_enabled") == "on" {
		interval, err := strconv.Atoi(v.Get("periodic_interval_seconds"))
		if err != nil {
			return api.SaveProfileDeploymentPolicyRequest{}, err
		}
		confirmations, err := strconv.Atoi(v.Get("periodic_confirmations"))
		if err != nil {
			return api.SaveProfileDeploymentPolicyRequest{}, err
		}
		periodic = &api.PeriodicProfilePolicy{IntervalSeconds: interval, Confirmations: confirmations}
	}
	gate, err := profileGatePolicyForm(v)
	if err != nil {
		return api.SaveProfileDeploymentPolicyRequest{}, err
	}
	return api.SaveProfileDeploymentPolicyRequest{ExpectedRevision: req.ExpectedRevision, Config: api.ProfileDeploymentPolicyConfig{CanaryGate: gate, Periodic: periodic, NotifyRouteRegressions: v.Get("enabled") == "on" && v.Get("notify_route_regressions") == "on", Enabled: v.Get("enabled") == "on", Runtime: v.Get("runtime"), WindowSeconds: window, WarmupSeconds: warmup, Options: *req.Options}}, nil
}

func (s *server) profileDeploymentCheckBanner(r *http.Request, acct state.Account, app state.App, id string) *api.ProfileDeploymentCheck {
	store, ok := s.store.(state.ProfileDeploymentCheckStore)
	if !ok {
		return nil
	}
	check, err := store.GetProfileDeploymentCheck(r.Context(), acct.ID, app.ID, id)
	if err != nil {
		return nil
	}
	check = profileDeploymentCheckLinks(app.Slug, check)
	return &check
}

func (s *server) loadPeriodicMonitorHistory(ctx context.Context, account string, app state.App, view *dashboard.ProfileDeploymentChecksView) {
	if store, ok := s.store.(state.ProfilePeriodicStore); ok {
		rows, err := store.ListProfilePeriodicMonitors(ctx, account, app.ID)
		if err != nil {
			view.Error = "Periodic history could not be listed."
			return
		}
		for _, m := range rows {
			view.Monitors = append(view.Monitors, profilePeriodicLinks(app.Slug, m))
		}
	}
}

func profileGatePolicyForm(v url.Values) (*api.ProfileCanaryGatePolicy, error) {
	if v.Get("enabled") != "on" || v.Get("canary_gate_enabled") != "on" {
		return nil, nil
	}
	confirmations, err1 := strconv.Atoi(v.Get("canary_gate_confirmations"))
	timeout, err2 := strconv.Atoi(v.Get("canary_gate_timeout_seconds"))
	if err1 != nil {
		return nil, err1
	}
	if err2 != nil {
		return nil, err2
	}
	return &api.ProfileCanaryGatePolicy{Confirmations: confirmations, TimeoutSeconds: timeout, OnTimeout: v.Get("canary_gate_on_timeout"), AutoRollback: v.Get("canary_gate_auto_rollback") == "on"}, nil
}
