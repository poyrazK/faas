package main

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

const (
	dashboardAccountPlanAction     = "account_plan"
	dashboardAccountPlanCSRFCookie = "faas_csrf_account_plan"
)

// dashboardAccountPlan handles the plan selector on /dashboard/account.
// The account page is intentionally only a router: paid upgrades enter the
// existing hosted-checkout confirmation flow, and every subscription-backed
// change enters the provider portal. The local entitlement changes only after
// provider confirmation through the billing webhook.
func (s *server) dashboardAccountPlan(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticatedNamed(
		s.sessions, r, dashboardAccountPlanAction, acct.ID, dashboardAccountPlanCSRFCookie); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad form", err.Error()))
		return
	}
	target := api.Plan(strings.ToLower(strings.TrimSpace(r.FormValue("plan"))))
	if !target.Valid() {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad plan", "plan must be free|hobby|pro|scale"))
		return
	}
	if acct.Plan == target {
		http.Redirect(w, r, "/dashboard/account?plan=unchanged", http.StatusSeeOther)
		return
	}
	if acct.Plan.RequiresBillingUpgradeTo(target) {
		http.Redirect(w, r, "/dashboard/upgrade?plan="+url.QueryEscape(string(target)), http.StatusSeeOther)
		return
	}
	portalURL := s.billingPortalURLForProvider(r.Context(), acct)
	if portalURL == "" {
		http.Redirect(w, r, "/dashboard/account?plan=unavailable", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, portalURL, http.StatusSeeOther)
}
