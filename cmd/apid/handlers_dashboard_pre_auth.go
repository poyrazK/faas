package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

func parseAppPreAuthPath(rest string) (string, bool) {
	slug, ok := strings.CutSuffix(rest, "/pre-auth")
	if !ok || slug == "" || strings.Contains(slug, "/") {
		return "", false
	}
	return slug, true
}

// renderAppPreAuth shows the configured policy and the same scoped observation
// rows as GET /v1/apps/{slug}/pre-auth-observations. It has no mutation path.
func (s *server) renderAppPreAuth(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, slug string) {
	app, err := s.store.AppBySlug(r.Context(), slug)
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	rng := r.URL.Query().Get("range")
	if !appmetrics.IsValidRange(rng) {
		rng = appmetrics.DefaultRange
	}
	config := app.Manifest.PreAuthRateLimit
	view := dashboard.PreAuthProtectionData{
		AppSlug:      app.Slug,
		Config:       config,
		Configured:   config != nil && config.Mode != api.PreAuthRateLimitOff,
		Observations: s.appPreAuthObservations(r.Context(), app, rng),
		NoActivity:   true,
	}
	for _, value := range appmetrics.Ranges() {
		view.RangeOptions = append(view.RangeOptions, dashboard.PreAuthRangeOption{Value: value, Selected: value == rng})
	}
	for _, policy := range view.Observations.Policies {
		if policy.Kind == "targets" {
			view.TargetPolicies = append(view.TargetPolicies, policy)
		} else {
			view.DecisionPolicies = append(view.DecisionPolicies, policy)
		}
		if policy.WouldBlock != 0 || policy.Result2xx != 0 || policy.Result3xx != 0 ||
			policy.Result4xx != 0 || policy.Result5xx != 0 || policy.ResultUnknown != 0 ||
			policy.TargetFailures != 0 || policy.TargetThreshold != 0 || policy.TargetMissing != 0 ||
			policy.TargetInvalid != 0 || policy.TargetFallback != 0 {
			view.NoActivity = false
		}
	}
	page := dashboard.Page{
		Title: app.Slug + " pre-auth protection", Body: "pre_auth",
		Account: dashboardAccountView(acct, 0), Data: view,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}
