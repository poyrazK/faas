package main

// Dashboard hosted-checkout hand-off (Free → paid upgrade).
//
// Before this file the only way for a customer to reach the billing
// provider's checkout was the 402 `checkout_url` extension on
// PATCH /v1/account/plan, which neither the dashboard nor the CLI's
// text mode rendered. The billing page pointed Free customers at the
// CLI, and the CLI printed the 402 code + detail without the URL.
//
// Two routes close that:
//
//	GET  /dashboard/upgrade?plan=<hobby|pro|scale>  confirmation page
//	POST /dashboard/upgrade                          303 → provider checkout
//
// The confirmation page exists because the dashboard CSRF cookie
// (faas_csrf) carries exactly one sealed token per response, and the
// billing page already spends it on the spend-cap form. Giving the
// checkout form its own page — the same shape as the account delete
// confirmation — keeps the one-token-per-page contract intact.
//
// The POST reuses beginHostedCheckout (billing_checkout.go), the same
// routine PATCH /v1/account/plan calls, so the dashboard and the CLI
// cannot drift on customer creation or checkout semantics. The account
// plan is NOT changed here: the provider webhook remains the only path
// onto a paid plan (spec §4.7).

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardUpgradeAction is the CSRF envelope action for the checkout
// form. Verified by dashboardUpgrade against (action, account_id).
const dashboardUpgradeAction = "upgrade_plan"

// canStartCheckout mirrors hostedCheckoutAvailable without a target
// plan: true when the provider exposes hosted checkout and the account
// has no subscription yet. The billing page uses it to decide between
// the per-plan upgrade links and the CLI / portal hint.
func (s *server) canStartCheckout(acct state.Account) bool {
	return s.billingProvider != nil &&
		s.billingProvider.Capabilities().Has(billing.CapHostedCheckout) &&
		acct.StripeSubscriptionItem == ""
}

// upgradeOptionsFor lists the paid plans acct can upgrade to, in
// ascending rank, with the display fields the templates need. Limits
// come from pkg/api/limits.go (the only quota table); prices are
// formatted here so the template never sees millicents.
func upgradeOptionsFor(acct state.Account) []dashboard.UpgradeOption {
	out := make([]dashboard.UpgradeOption, 0, len(api.Plans))
	for _, p := range api.Plans {
		if !acct.Plan.RequiresBillingUpgradeTo(p) {
			continue
		}
		l := api.MustLimitsFor(p)
		out = append(out, dashboard.UpgradeOption{
			Plan:            string(p),
			PriceFormatted:  formatPriceEuros(l.PriceMillicents),
			IncludedGBHours: int64(l.IncludedGBHours),
			RAMMB:           l.RAMMB,
			MaxConcurrency:  l.MaxConcurrency,
			DeployedApps:    l.DeployedApps,
		})
	}
	return out
}

// upgradeNoticeFor maps the ?upgrade= redirect flag set by
// dashboardUpgrade onto the customer-facing banner. Unknown values
// render nothing so a stale bookmark cannot inject copy.
func upgradeNoticeFor(flag string) string {
	switch flag {
	case "error":
		return "We could not start checkout with the billing provider. Nothing was charged — please try again in a minute."
	case "unavailable":
		return "Hosted checkout is not available for this account. Use the billing portal or the CLI to change plan."
	case "invalid_plan":
		return "Choose one of the paid plans below."
	default:
		return ""
	}
}

// providerLabel is the customer-facing brand for the closed provider
// name set (pkg/billing/loader). Unknown / empty → neutral copy.
func providerLabel(name string) string {
	switch name {
	case "polar":
		return "Polar"
	case "paddle":
		return "Paddle"
	case "stripe":
		return "Stripe"
	default:
		return "the billing provider"
	}
}

// renderUpgrade renders GET /dashboard/upgrade. With a valid ?plan= it
// shows the plan summary + the checkout form; without one it shows the
// eligible plans as a chooser. The CSRF token is minted here and set
// on the faas_csrf cookie, matching the delete / raise-cap renderers.
func (s *server) renderUpgrade(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account) {
	appCount, err := s.store.CountDeployedApps(r.Context(), acct.ID)
	if err != nil {
		log.Warn("dashboard renderUpgrade: count deployed apps", "account_id", acct.ID, "err", err)
		appCount = 0
	}
	data := s.upgradePageData(r, acct)
	if data.Available {
		tok, err := middleware.IssueForAuthenticated(s.sessions, dashboardUpgradeAction, acct.ID)
		if err != nil {
			log.Error("dashboard renderUpgrade: csrf issue", "account_id", acct.ID, "err", err)
			renderProblem(w, log, err)
			return
		}
		data.ConfirmToken = tok
		http.SetCookie(w, &http.Cookie{
			Name:     middleware.CookieNameAuthenticated,
			Value:    tok,
			Path:     "/",
			HttpOnly: true,
			Secure:   s.domain != "",
			SameSite: http.SameSiteLaxMode,
			MaxAge:   int(middleware.DefaultCSRFTTL.Seconds()),
		})
	}
	page := dashboard.Page{Title: "Upgrade", Body: "upgrade", Account: dashboardAccountView(acct, appCount), Data: data}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, log, err)
	}
}

// upgradePageData builds the UpgradeData view: resolves ?plan= against
// the eligible options and explains why checkout is unavailable when
// it is. Kept separate from renderUpgrade so the renderer stays under
// the 50-line handler guideline.
func (s *server) upgradePageData(r *http.Request, acct state.Account) dashboard.UpgradeData {
	name := providerName(s.billingProvider)
	data := dashboard.UpgradeData{
		CurrentPlan:   string(acct.Plan),
		Options:       upgradeOptionsFor(acct),
		Notice:        upgradeNoticeFor(r.URL.Query().Get("upgrade")),
		Provider:      name,
		ProviderLabel: providerLabel(name),
	}
	target := api.Plan(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("plan"))))
	for i := range data.Options {
		if data.Options[i].Plan == string(target) {
			data.Target = &data.Options[i]
		}
	}
	switch {
	case acct.StripeSubscriptionItem != "":
		data.Reason = "Your account already has a subscription. Plan changes on an existing subscription are made in the " + data.ProviderLabel + " portal."
		data.PortalURL = s.billingPortalURLForProvider(r.Context(), acct)
	case s.billingProvider == nil || !s.billingProvider.Capabilities().Has(billing.CapHostedCheckout):
		data.Reason = "Hosted checkout is not available on this deployment. Change plan via the CLI: faas plan <plan>."
	case len(data.Options) == 0:
		data.Reason = "Your account is already on the highest plan."
	case data.Target == nil:
		// Chooser mode: the template lists Options.
	default:
		data.Available = true
	}
	return data
}

// dashboardUpgrade handles POST /dashboard/upgrade. Verifies the CSRF
// envelope, starts the hosted checkout through beginHostedCheckout, and
// hands the browser to the provider with a 303. Failures redirect back
// to the billing page with a ?upgrade= flag the template turns into a
// banner; nothing about the local account changes on this path.
func (s *server) dashboardUpgrade(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if err := middleware.VerifyAuthenticated(s.sessions, r, dashboardUpgradeAction, acct.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid CSRF token", "please reload the page and try again"))
		return
	}
	if err := r.ParseForm(); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad form", err.Error()))
		return
	}
	plan := api.Plan(strings.ToLower(strings.TrimSpace(r.FormValue("plan"))))
	if !plan.Valid() || !plan.IsPaid() {
		http.Redirect(w, r, "/dashboard/upgrade?upgrade=invalid_plan", http.StatusSeeOther)
		return
	}
	txID, checkoutURL, err := s.beginHostedCheckout(r.Context(), acct, plan)
	switch {
	case errors.Is(err, errNoHostedCheckout):
		http.Redirect(w, r, "/dashboard/billing?upgrade=unavailable", http.StatusSeeOther)
		return
	case err != nil:
		s.log.Error("dashboard upgrade: begin checkout",
			"account", acct.ID, "target_plan", logsanitize.Field(string(plan)), "err", err)
		http.Redirect(w, r, "/dashboard/billing?upgrade=error", http.StatusSeeOther)
		return
	}
	s.audit.Emit(r.Context(), "billing.checkout_started", &acct.ID, map[string]any{
		"target_plan": string(plan),
		"tx_id":       txID,
		"via":         "dashboard",
		"at":          time.Now().UTC().Format(time.RFC3339),
	})
	http.Redirect(w, r, checkoutURL, http.StatusSeeOther)
}
