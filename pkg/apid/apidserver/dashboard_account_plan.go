package apidserver

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
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
		writeDashboardUnauthorized(w, r)
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
		// The upgrade page shows a scheduled cancellation or downgrade and
		// can undo it (ADR-20261103131742 amendment 1).
		if notice, _ := s.scheduledChangeNotice(r.Context(), acct); notice != "" {
			http.Redirect(w, r, "/dashboard/upgrade", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/dashboard/account?plan=unchanged", http.StatusSeeOther)
		return
	}
	if acct.Plan.RequiresBillingUpgradeTo(target) {
		http.Redirect(w, r, "/dashboard/upgrade?plan="+url.QueryEscape(string(target)), http.StatusSeeOther)
		return
	}
	s.requireStepUpHandler(5*time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.dashboardApplyAccountPlan(w, r, acct, target)
	})).ServeHTTP(w, r)
}

func (s *server) dashboardApplyAccountPlan(w http.ResponseWriter, r *http.Request, acct state.Account, target api.Plan) {
	// The customer portal no longer switches plans (ADR-20261103131742), so
	// a downgrade is scheduled with the provider here, as the API does.
	if flag, ok := s.dashboardScheduleDowngrade(r.Context(), acct, target); ok {
		http.Redirect(w, r, "/dashboard/billing?upgrade="+flag, http.StatusSeeOther)
		return
	}
	portalURL := s.billingPortalURLForProvider(r.Context(), acct)
	if portalURL == "" {
		http.Redirect(w, r, "/dashboard/account?plan=unavailable", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, portalURL, http.StatusSeeOther)
}

// dashboardScheduleDowngrade schedules acct's downgrade to target with the
// provider and returns the ?upgrade= flag for the billing page. ok is false
// when acct has no subscription the provider changes through its API, and the
// caller falls back to the portal. The plan checks are the API's: a subject
// over the target plan's limits is refused before the provider is asked.
func (s *server) dashboardScheduleDowngrade(ctx context.Context, acct state.Account, target api.Plan) (string, bool) {
	billingAcct, changer, ok, err := s.subscriptionChanger(ctx, acct)
	if err != nil {
		return "downgrade_failed", true
	}
	if !ok {
		return "", false
	}
	if problem := s.planDowngradeProblem(ctx, acct, target); problem != nil {
		return "downgrade_blocked", true
	}
	if _, err := changer.ChangeSubscriptionPlan(ctx, billingAcct, target); err != nil {
		s.log.Error("dashboard downgrade: change subscription", "account", acct.ID,
			"target_plan", logsanitize.Field(string(target)), "err", logsanitize.FieldAny(err))
		return planChangeFlag(err, "downgrade_failed"), true
	}
	s.audit.Emit(ctx, "billing.plan_change_requested", &acct.ID, map[string]any{
		"target_plan": string(target), "via": "dashboard",
	})
	return "downgrade_scheduled", true
}
