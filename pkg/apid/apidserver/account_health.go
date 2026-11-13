package apidserver

// Customer account health (ADR-20261112210000):
//
//	GET /v1/account/health       — JSON for the calling account
//	GET /dashboard/account/health — the same view as a dashboard page
//
// Both derive from the operator diagnosis (buildTenantDiagnosis) and pass
// it through customerAccountHealth, which drops internal detail: abuse-hold
// reasons, platform flags and replica drop counters never reach customers.

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// customerRestrictionCopy is the customer wording for each restriction kind.
// A kind missing here is not shown to customers, so a new internal kind
// stays internal until it is given customer wording.
var customerRestrictionCopy = map[string]api.AccountHealthRestriction{
	api.RestrictionAccountSuspended: {Title: "Your account is suspended",
		Detail: "Apps are stopped and changes are refused until the account is restored.",
		Action: "Review billing", ActionURL: "/dashboard/billing"},
	api.RestrictionAccountPastDue: {Title: "A payment failed",
		Detail: "Update your payment method before the grace period ends to keep your apps running.",
		Action: "Update payment method", ActionURL: "/dashboard/billing"},
	api.RestrictionDeletionPending: {Title: "Account deletion is scheduled",
		Detail: "The account and its apps will be deleted at the end of the grace period unless you restore it.",
		Action: "Restore account", ActionURL: "/dashboard/account"},
	api.RestrictionAbuseHold: {Title: "Your account is on hold",
		Detail: "Some activity is paused while Gregale reviews the account. Contact support to resolve the hold.",
		Action: "Contact support", ActionURL: "/dashboard/support#new-ticket"},
	api.RestrictionEmailUnverified: {Title: "Verify your email address",
		Detail: "Some actions are refused until your email address is verified.",
		Action: "Resend verification email", ActionURL: "/dashboard/account"},
	api.RestrictionOverageCapReached: {Title: "Overage cap reached",
		Detail: "This month's usage beyond your plan reached your overage cap. Raise the cap to keep scaling.",
		Action: "Raise the cap", ActionURL: "/dashboard/billing"},
	api.RestrictionPlatformReleasePause: {Title: "New deployments are paused",
		Detail: "Gregale has temporarily paused new deployments for all accounts. Running apps are not affected."},
}

// customerAccountHealth maps an operator diagnosis to the customer view.
func customerAccountHealth(d api.ObsTenantDiagnosis) api.AccountHealth {
	h := api.AccountHealth{
		GeneratedAt:       d.GeneratedAt,
		Restrictions:      []api.AccountHealthRestriction{},
		FailedDeployments: []api.AccountHealthDeployment{},
		DomainIssues:      []api.AccountHealthDomain{},
		RefusedRequests: api.AccountHealthRequests{
			WindowHours: d.CustomerErrors.WindowHours,
			Total:       d.CustomerErrors.Total,
			ByCode:      []api.AccountHealthCode{},
			Recent:      []api.AccountHealthRequest{},
		},
		Unavailable: d.Unavailable,
	}
	seen := map[string]bool{}
	for _, r := range d.Restrictions {
		wording, ok := customerRestrictionCopy[r.Kind]
		// One entry per kind: an account hold and an organization hold
		// read the same to the customer.
		if !ok || seen[r.Kind] {
			continue
		}
		seen[r.Kind] = true
		wording.Kind, wording.Since = r.Kind, r.Since
		h.Restrictions = append(h.Restrictions, wording)
	}
	for _, f := range d.FailedDeployments {
		h.FailedDeployments = append(h.FailedDeployments, api.AccountHealthDeployment{
			DeploymentID: f.DeploymentID, AppID: f.AppID, AppSlug: f.AppSlug, ErrorCode: f.ErrorCode,
			Error: f.Error, Hint: f.Hint, CreatedAt: f.CreatedAt})
	}
	for _, issue := range d.DomainIssues {
		h.DomainIssues = append(h.DomainIssues, api.AccountHealthDomain{
			Domain: issue.Domain, AppSlug: issue.AppSlug, Problem: issue.Problem, CertError: issue.CertLastError})
	}
	for _, c := range d.CustomerErrors.ByCode {
		h.RefusedRequests.ByCode = append(h.RefusedRequests.ByCode, api.AccountHealthCode(c))
	}
	for _, e := range d.CustomerErrors.Recent {
		h.RefusedRequests.Recent = append(h.RefusedRequests.Recent, api.AccountHealthRequest(e))
	}
	h.Attention = len(h.Restrictions) + len(h.FailedDeployments) + len(h.DomainIssues)
	return h
}

// accountHealthFor builds the customer view for acct. Billing is needed
// only for the overage-cap restriction; when it cannot be read the cap is
// skipped and "billing" is listed as unavailable.
func (s *server) accountHealthFor(ctx context.Context, r *http.Request, acct state.Account) api.AccountHealth {
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	billing, err := projectObsTenantBilling(r.WithContext(ctx), s.store, s.billingProvider, acct, month)
	diagnosis := s.buildTenantDiagnosis(ctx, acct, billing)
	if err != nil {
		diagnosis.Unavailable = append(diagnosis.Unavailable, "billing")
	}
	return customerAccountHealth(diagnosis)
}

// getAccountHealth handles GET /v1/account/health.
func (s *server) getAccountHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	writeJSON(w, http.StatusOK, s.accountHealthFor(r.Context(), r, acct))
}

// renderAccountHealth renders /dashboard/account/health.
func (s *server) renderAccountHealth(w http.ResponseWriter, r *http.Request, _ *slog.Logger, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	s.renderCustomDashboardPage(w, r, acct, "Account health", "account_health", s.accountHealthFor(r.Context(), r, acct))
}

// accountAttention is the cheap restriction count for the overview banner:
// it reads only the account row and the platform release pause, never the
// deployment, domain or error tables.
func (s *server) accountAttention(acct state.Account) int {
	diagnosis := api.ObsTenantDiagnosis{Restrictions: accountRestrictions(acct, api.ObsTenantBilling{})}
	if s.platformReleasePaused() {
		diagnosis.Restrictions = append(diagnosis.Restrictions, api.ObsTenantRestriction{Kind: api.RestrictionPlatformReleasePause})
	}
	return len(customerAccountHealth(diagnosis).Restrictions)
}
